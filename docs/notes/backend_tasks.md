# Задачи для бэкенда (по итогам интеграционного прогона 2026-07-14)

Прогнали ваш Go-бэкенд сквозным тестом поверх ML-сервиса: `register → chat →
SSE-стрим → final_result → persist → /objects → /geo/layers`. **Путь целиком
рабочий, контракт с `habitus/online/schema.py` соблюдён.** Ниже — что нужно
закоммитить/доделать, по приоритету.

---

## 🔴 Блокеры — без этого стек не заводится из коробки

### 1. Баг в `Dockerfile.ml` — сборка ML-сервиса падает
Порядок слоёв ломает `uv sync`: манифесты копируются, `uv sync` пытается
собрать сам пакет `habitus` как editable, но исходников ещё нет → hatchling
падает (`Failed to build habitus @ file:///app`).

**Фикс (стандартный uv-паттерн, кэширование слоёв сохраняется):**
```dockerfile
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project   # только зависимости

COPY habitus ./habitus
RUN uv sync --frozen --no-dev                        # добить проект после COPY
```
Проверено — с этим образ собирается (`Built habitus`, `+ habitus==0.1.0`).

### 2. Ключи не прокидываются в `ml-service` → LLM молчит
В `docker-compose.yml` у `ml-service` в `environment` нет `OPENROUTER_API_KEY`
(и `ORS_API_KEY`). Без ключа сервис деградирует (`degraded: ["nlu","llm"]`):
шаблонные объяснения, `parsed` пустой, структурные фильтры не работают.

**Решить как прокидывать секреты** — через `--env-file`, проброс переменных в
`environment`, или Docker secrets. У себя мы держим локальный
`docker-compose.override.yml` (gitignored) — можно взять как образец, но для
прода нужно ваше решение.

### 3. Cold-start: модели качаются 4.6 ГБ на первом `/search`
В свежем контейнере BGE-M3 + reranker не закэшированы → первый `/search`
качает ~4.6 ГБ с HuggingFace, не влезает в `ML_SEARCH_TIMEOUT_S=60` (→
`llm_timeout`), и при малом диске **забивает его и вешает docker-демон**.

**Варианты:** примонтировать/пре-загрузить кэш моделей
(`~/.cache/huggingface` → `/root/.cache/huggingface`), либо `RUN` прогрев в
Dockerfile при сборке, либо увеличить старт-таймаут для первого прогона.
Ваш warm-up при старте бэкенда идею закрывает частично, но упирается в тот же
таймаут при холодном кэше.

---

## 🟡 Функциональные пробелы — пайплайн умеет, бэкенд не использует

### 4. Point-поиск не проброшен
`SearchRequest` в ML принимает опциональный `point` (lon/lat/minutes/mode) —
это «квартиры рядом со мной» / клик по карте. Бэкенд шлёт только `{query}`:
`streamRequest` (в `stream_handler.go`) содержит лишь `Text`, а
`search_stream_service.go` вызывает `Search(..., SearchRequest{Query: text})`
без `Point`. **Фича гео-поиска от точки не доходит до ML.** Добавить `point`
в тело запроса стрима и пробросить в `client.SearchRequest`.

### 5. Слой `metro` не отдаётся в `/geo/layers`
`geo_layers_service.go` → `layerKinds` маппит только `schools/bars/parks`.
В таблице `poi` лежит **276 станций метро** (`kind='metro'`), но слоя `metro`
в маппинге нет → запрос тихо отбрасывается. Для риелторского сервиса, где
«пешком до метро» — ключевая ось, странно. Добавить `"metro": {"metro"}`
(и заодно сверить имена слоёв с `frontend/Пайплайн фронт.md §5`).

### 6. `data_freshness` копится, но не показывается
ML отдаёт «данные актуальны на …», бэкенд сохраняет в `chat_searches`, но в
ответ пользователю не выводит (помечено у вас как follow-up Н.1). Не блокер —
напоминание, что поле готово к выводу без новой миграции.

---

## Приоритет
1. **Сначала #1 и #2** — иначе `docker compose up` из репы у любого коллеги не
   заведётся с рабочим LLM.
2. **#3** — важно для прода/CI (первый запрос не должен таймаутиться/ронять диск).
3. **#4, #5** — функциональные, планировать в следующий проход.
4. **#6** — по готовности фронта.

## Что уже проверено рабочим (на всякий случай — это НЕ трогать)
Auth/сессии, CRUD чатов, SSE-события (agent_status/text_token/chat_renamed/
final_result/stream_end), сборка `final_result` (10 объектов с
price_from/area_sqm/tags/match_score/floor + suggested_areas convex hull),
persist в `chat_searches`/`chat_search_results`, `/objects/{id}?chat_id=`,
graceful degradation при недоступном LLM. Контракт DTO ↔ `schema.py` — точный.
