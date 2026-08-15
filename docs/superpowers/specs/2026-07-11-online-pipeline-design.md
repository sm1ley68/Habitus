# Online-фаза (Фаза 2): дизайн ML-пайплайна обработки запроса

**Дата:** 2026-07-11
**Статус:** утверждён к реализации
**Зона:** ML/Data (я). Беки владеют gateway (Go/Fiber), деплоем, инфраструктурой БД, транспортом.
**Предшествует:** offline-фаза (Фаза 1) завершена — `listings` с структурными полями, `geom`, `embedding vector(1024)`, `sparse_embedding sparsevec(250002)`, `content_hash`; таблица `poi`; BGE-M3 через FlagEmbedding.

## 1. Цель и границы

Online-фаза превращает свободный текст пользователя в ранжированный список объектов недвижимости + объяснение — за секунды, обращаясь **только к готовой БД** (в интернет на лету не ходит, кроме вызовов LLM-провайдера и опционального изохрон-провайдера).

Ключевой принцип из спеки: **своя модель не обучается**. Ценность — в оркестрации агентов и гибридном поиске (RAG) поверх заранее собранной базы. Всё «ML» — это инференс готовых моделей (BGE-M3 dense+sparse, bge-reranker-v2-m3, Qwen через OpenRouter).

**Что я делаю:** библиотека `habitus/online/*` (весь пайплайн) + тонкий FastAPI `POST /search` поверх неё. Eval-харнесс. Golden-set.
**Что не трогаю:** gateway Go/Fiber, деплой, миграции/бэкапы БД-сервера, транспорт между Go и Python.

### Зафиксированные развилки

| Развилка | Решение |
|---|---|
| FastAPI-граница | Библиотека + тонкий FastAPI (`POST /search`, `/health`). Gateway/деплой — беки. |
| Гео-изохроны | Интерфейс `IsochroneProvider`. Дефолт: готовые колонки `walk_min_*` + PostGIS `ST_DWithin`. Для кастомной точки — реальный ORS-клиент. Беки могут подменить своим клиентом через тот же интерфейс. |
| LLM-доступ | `LLMClient` protocol → реальный `OpenRouterLLM` (Qwen, temp=0, фолбэк-цепочка) + `FakeLLM` для тестов. Юниты — на фейке, живой smoke — на реальной модели (ключ у пользователя). |
| Golden-set | Черновик 30-50 реальных запросов (RU+EN) составляю я, пользователь правит. |

## 2. Контракт (Pydantic-схемы)

`habitus/online/schema.py` — единственный источник правды по формам данных.

### `ParsedQuery` — выход Linguistic Agent

Свободный текст → структура. **Две части:** жёсткие фильтры (в SQL WHERE) + семантический остаток (в вектор).

```python
class GeoConstraint(BaseModel):
    kind: Literal["school", "metro", "park"]
    walk_minutes: int          # порог пешей доступности

class ParsedQuery(BaseModel):
    price_min: int | None = None
    price_max: int | None = None
    rooms: list[int] | None = None            # [1,2] = «1-2 комнаты»
    area_min: float | None = None
    area_max: float | None = None
    geo: list[GeoConstraint] = []
    window_orientation: list[str] = []        # ["SW","W"]
    noise_max: Literal["low","medium","high"] | None = None
    stop_factors: list[str] = []              # ["bars","communal_flats"]
    semantic_text: str = ""                   # остаток для dense/sparse («двор-колодец»)
    lang: Literal["ru","en"] = "ru"
```

Инвариант: любое поле опционально — модель извлекает только то, что явно сказано. `semantic_text` — то, что не легло в структуру (для гибридного поиска).

### `ResultItem` / `SearchResponse` — выход пайплайна

```python
class ResultItem(BaseModel):
    external_id: str
    price: int | None
    area: float | None
    rooms: int | None
    address_facts: dict          # walk_min_*, bar_density_500m, noise_level, orientation — факты для объяснения
    score: float                 # финальный score после реранка

class SearchResponse(BaseModel):
    results: list[ResultItem]
    explanation: str             # только поверх фактов из БД
    parsed: ParsedQuery          # что поняли (прозрачность/дебаг)
    relaxed: list[str] = []      # какие ограничения ослаблены relaxation-петлёй
    data_freshness: str          # «данные актуальны на …» (max updated_at)
    degraded: list[str] = []     # какие слои отвалились (вектор/reranker/LLM)
```

## 3. Компоненты и поток

```
POST /search {query, filters?, isochrones?}
   │
   ▼
Linguistic Agent (nlu.py) ── LLM function-calling → ParsedQuery (Pydantic + retry)
   │
   ▼
Orchestrator (orchestrator.py) ── маршрутизация + relaxation loop
   │
   ├─► Geo-агент (geo.py) ── IsochroneProvider → гео-фильтр (SQL)
   │
   ▼
Hybrid Retrieval (retrieval.py) ── WHERE-фильтр + гео + dense + sparse + RRF → top-K
   │
   ▼
Reranker (rerank.py) ── bge-reranker-v2-m3 → top-N осмысленный порядок
   │
   ▼
Explanation (explain.py) ── LLM только поверх фактов из БД
   │
   ▼
SearchResponse
```

### 3.1 `llm.py` — LLM-клиент
- `LLMClient` (Protocol): `complete(messages, tools?, temperature=0) -> LLMResponse`.
- `OpenRouterLLM`: реальный клиент через `openai`-SDK (OpenRouter OpenAI-совместим). Primary — Qwen; фолбэк-цепочка DeepSeek → GPT-4o-mini/Gemini Flash при ошибке/таймауте (массив моделей). temperature=0.
- `FakeLLM`: программируемые ответы для детерминированных тестов (без сети).
- Конфиг: `openrouter_api_key`, `llm_model`, `llm_fallbacks`, `llm_base_url`, таймауты.

### 3.2 `nlu.py` — Linguistic Agent
- Промпт + tool-схема (JSON-schema из `ParsedQuery`), few-shot краевые случаи (компромисс двух работ, кросс-язык EN→RU).
- Вызов LLM → парс JSON → `ParsedQuery.model_validate`.
- **Retry-петля:** невалидный JSON/схема → ошибку текстом обратно модели, 2-3 попытки (самопочинка).
- Языконезависимость: русский только в примерах; английский запрос парсится в те же поля.

### 3.3 `retrieval.py` — гибридный retrieval (сердце RAG)
- **WHERE-билдер:** из `ParsedQuery` → параметризованный SQL. price/rooms/area → сравнения; `geo` → `walk_min_school/metro/park <= walk_minutes`; `noise_max` → `noise_level`; `window_orientation` → пересечение массива; `stop_factors` (`bars`) → `bar_density_500m = 0`; всегда `is_active = TRUE`.
- **Кодирование запроса:** `semantic_text` через существующий BGE-M3 (`habitus.embed.encode`) → dense + sparse.
- **Dense-канал:** `ORDER BY embedding <=> :qvec` с `SET LOCAL hnsw.iterative_scan = strict_order` (грабля фильтрованного HNSW — при жёстких WHERE индекс отдаёт < LIMIT).
- **Sparse-канал:** `ORDER BY sparse_embedding <=> :qsparse` (лексический матчинг «школа 239», «ЖК Символ»).
- **RRF-слияние:** `score = Σ 1/(60+rank_i)` по двум спискам → top-K кандидатов. `k=60` конфигурируемо.
- Чистые функции (RRF, WHERE-билдер) юнит-тестируемы без БД; сам поиск — интеграционно на реальном Postgres.

### 3.4 `geo.py` — Geo-Spatial Agent
- `IsochroneProvider` (Protocol): `isochrone(point, minutes, mode) -> polygon (WKT/GeoJSON)`.
- Дефолт `PrecomputedProvider`: гео-ограничения решаются готовыми колонками `walk_min_*` + `ST_DWithin` (без сети).
- `ORSProvider`: реальный клиент OpenRouteService/Valhalla для кастомной точки (компромисс «Сколково↔Сити» — центральная точка + изохрона на лету). Конфиг `ors_base_url`, `ors_api_key`.
- Центральная точка для компромиссных запросов: центроид/оптимум между двумя точками интереса.
- Вывод: SQL-гео-предикат (`ST_DWithin(geom, :pt, :r)` или `ST_Within(geom, :polygon)`), встраивается в retrieval.

### 3.5 `rerank.py` — Reranker
- `bge-reranker-v2-m3` через `FlagReranker` (мультиязычный, русский). Ленивая загрузка (как `get_model` в embed).
- Вход: (запрос, `doc_text` кандидата) пары для top-K из retrieval → скоры → top-N.
- Конфиг `reranker_model`, `rerank_top_n`.

### 3.6 `orchestrator.py` — Оркестратор + relaxation loop
- Маршрутизация: обычный запрос → сразу фильтры+вектор; запрос с кастомной геоточкой → сначала Geo-агент, потом retrieval.
- **Relaxation loop:** результатов < порога → ослабляем по приоритету (расширить `walk_minutes`, поднять `price_max`, снять «мягкие» фильтры вроде orientation) → повторить retrieval, до N итераций. Каждое ослабление пишется в `SearchResponse.relaxed` для честного объяснения.
- Логика на одной LLM/детерминированных правилах достаточна для демо; масштаб до LangGraph — «сюда масштабируемся».

### 3.7 `explain.py` — Объясняющая генерация
- Вход: факты top-N (адрес-факты, `walk_min_*`, `bar_density`, orientation, что ослаблено).
- LLM формулирует ответ **строго поверх переданных фактов** (промпт запрещает вводить данные не из контекста). Ноль галлюцинаций адресов.
- Деградация: LLM недоступна → шаблонный ответ из фактов.

### 3.8 `pipeline.py` — сборка + деградация по слоям
- End-to-end: parse → orchestrate → retrieve → rerank → explain → `SearchResponse`.
- **Деградация:** вектор недоступен → SQL+гео фильтры (хуже, но живо); reranker упал → порядок retrieval; LLM упала → шаблонное объяснение. Каждый отвалившийся слой → в `SearchResponse.degraded`.
- `data_freshness` = `max(updated_at)` по выдаче.

### 3.9 `service.py` — FastAPI (тонкий)
- `POST /search` → `SearchResponse`. `GET /health`.
- Только валидация входа + вызов `pipeline`. Никакой бизнес-логики. Deploy/gateway — беки.

### 3.10 `cache.py` — кэш
- Кэш эмбеддингов запросов и парса/объяснений по хэшу текста (повторные и golden-запросы летают; демо не зависит от живости OpenRouter в момент защиты).
- Реализация: лёгкий кэш (in-memory LRU + опц. таблица в Postgres). Инвалидация не нужна — ключ по хэшу входа.

### 3.11 `trace.py` — трейсинг
- Инструментация по шагам: parse → SQL → retrieval → rerank → generation. Структурный лог + опциональный Langfuse/Phoenix self-host (флаг конфига). Без него дебаг 6-шагового пайплайна вслепую.

## 4. Eval-харнесс (`habitus/eval/`) — #1 по ROI

- `queries.yaml`: 30-50 эталонных запросов (RU+EN) с ожидаемым парсом и, где возможно, ожидаемыми объектами.
- Метрики: **parse-accuracy** (запрос→JSON), **recall@10**, **NDCG@10** (после реранка).
- Раннер: прогон golden-set через пайплайн → таблица метрик + сравнение «с RRF vs без», «с reranker vs без» (слайд защиты).
- Отчёт: markdown/JSON с цифрами, защита от регрессий при правке промптов.
- Замечание про ground-truth: для parse — точный golden JSON; для retrieval-relevance — размеченное подмножество (реальные listings из БД, ручная разметка релевантности на небольшом наборе).

## 5. Изменения инфраструктуры

- **Config** (`habitus/config.py`): + `openrouter_api_key`, `llm_model`, `llm_fallbacks`, `llm_base_url`, `reranker_model`, `ors_base_url`, `ors_api_key`, `rrf_k`, `retrieval_top_k`, `rerank_top_n`, `relaxation_max_iters`, `langfuse_*` (опц.).
- **CLI** (`habitus/cli.py`): + `habitus search "<query>"` (локальный прогон), `habitus eval` (golden-set).
- **Зависимости** (`pyproject.toml`): `fastapi`, `uvicorn`, `openai`, `pyyaml`; опц. `langfuse`. Reranker — уже в `FlagEmbedding`.
- **Схема БД:** структурных изменений не требует (online читает готовые колонки). Возможна опц. таблица `query_cache`.

## 6. Тестирование (TDD)

- **Юниты (без сети/БД):** RRF-слияние, WHERE-билдер, retry-петля NLU (FakeLLM), relaxation-логика, гео-фильтр-билдер, grounding объяснения (FakeLLM), Pydantic-валидация схем.
- **Интеграция (реальный Postgres, `pytest-postgresql`):** retrieval на сидированных listings+эмбеддингах; проверка фильтрованного HNSW.
- **Smoke (живой LLM, skip без ключа):** полный пайплайн parse→…→explain на реальном Qwen.

## 7. Блоки реализации (ревью Opus после каждого → апрув → пуш)

1. **Фундамент** — config-расширение, `LLMClient` (Protocol + `OpenRouterLLM` + `FakeLLM`), Pydantic-схемы (`ParsedQuery`, `SearchResponse`, `ResultItem`).
2. **Гибридный retrieval** — RRF, WHERE-билдер, dense+sparse SQL, кодирование запроса, фильтрованный-HNSW guard. *(ядро RAG, чистый DB)*
3. **Linguistic Agent (NLU)** — промпт, tool-схема, парс, retry-петля, кросс-язык.
4. **Reranker** — bge-reranker-v2-m3, ленивая загрузка, top-N.
5. **Geo-агент** — `IsochroneProvider` (Precomputed + реальный ORS), центральная точка, SQL-гео-фильтр.
6. **Оркестратор** — маршрутизация + relaxation loop + трекинг ослаблений.
7. **Объясняющая генерация** — grounded-промпт, анти-галлюцинация, шаблон-фолбэк.
8. **Сборка** — `pipeline.py` (деградация по слоям), `service.py` (FastAPI), `cache.py`, `trace.py`, CLI `search`.
9. **Eval-харнесс** — golden-set (`queries.yaml`), метрики parse-acc/recall@10/NDCG@10, раннер, отчёт, CLI `eval`.

**Порядок:** retrieval (2) раньше NLU (3) — тестируется независимо на рукописных `ParsedQuery`. Eval (9) последним — сквозная проверка; черновик golden-set можно начать раньше для тюнинга промптов.

## 8. Критерии готовности

- `habitus search "тихо, юго-запад, школа рядом, без баров, бюджет бизнес"` возвращает осмысленный ранжированный список + объяснение поверх фактов БД.
- Английский запрос («quiet flat near a strong school») работает поверх русской базы.
- `habitus eval` печатает parse-accuracy / recall@10 / NDCG@10 и дельту «с RRF/reranker vs без».
- FastAPI `POST /search` отдаёт `SearchResponse`; `GET /health` живой.
- Каждый слой умеет отвалиться, не убив систему (деградация).
- Все тесты зелёные (юнит + интеграция; smoke — при наличии ключа).
