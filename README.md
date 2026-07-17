# Habitus

**ИИ-агент по недвижимости Москвы — «цифровой урбанист-детектив».** Превращает
запрос на естественном языке («тихая двушка рядом с метро до 40 млн, окна на
юго-запад») в ранжированный список квартир с grounded-объяснением и подробным
«досье» по каждому объекту (логистика семьи, социальное окружение, вид из окна,
инсоляция, шум) — строго поверх реальных данных, без выдумывания фактов.

Ключевой принцип: **своя модель не обучается**. Ценность — в оркестрации агентов
и гибридном RAG поверх готовой базы (готовые модели только инференсятся).

---

## Содержание

- [Архитектура](#архитектура)
- [Требования](#требования)
- [Быстрый старт (Docker)](#быстрый-старт-docker)
- [Первый запуск: чего ожидать](#первый-запуск-чего-ожидать)
- [Наполнение данными](#наполнение-данными)
- [Производительность и режим «нативный ML»](#производительность-и-режим-нативный-ml)
- [Локальная разработка](#локальная-разработка)
- [CLI-команды](#cli-команды)
- [Переменные окружения](#переменные-окружения)
- [Диагностика проблем](#диагностика-проблем)

---

## Архитектура

Четыре сервиса, поднимаются одной командой `docker compose up`:

| Сервис | Технологии | Порт | Роль |
|---|---|---|---|
| **db** | PostgreSQL 16 + PostGIS + pgvector | `5544` | Хранилище: объявления, эмбеддинги, POI, гео-слои |
| **ml-service** | Python 3.12 / FastAPI | `8000` | ML-ядро: поиск, досье, Q&A. Внутренний, наружу не смотрит |
| **backend** | Go / Fiber | `8080` | Публичный API `/api/v1`: чаты, сессии, SSE-стриминг |
| **frontend** | Next.js | `3000` | Веб-интерфейс. Ходит только в Go-шлюз |

```
браузер → frontend (3000) → backend (8080) → ml-service (8000) → db (5544)
                                                     ↓
                                    BGE-M3 (эмбеддинги) + bge-reranker
                                    OpenRouter (Qwen-72B: разбор запроса + объяснение)
```

**Поток поиска:** NLU (разбор запроса LLM → структура) → гибридный retrieval
(SQL-фильтры + dense + sparse + RRF) → proximity-rerank + кросс-энкодер →
grounded-объяснение. Каждый слой умеет деградировать, не роняя систему.

Единственный источник правды по формам данных между сервисами —
`habitus/online/schema.py` (Python) ↔ `backend/internal/service/` (Go) ↔
`frontend/lib/agent/types.ts` (TS). Координаты везде `[lng, lat]`, WGS84.

---

## Требования

- **Docker** (Desktop на macOS/Windows или Engine на Linux) с ≥ 8 ГБ памяти,
  выделенной контейнерам.
- **≥ 25 ГБ свободного места на диске.** ML-образ ~9 ГБ (PyTorch), модели
  BGE-M3 + reranker ~4.6 ГБ, образы БД/фронта. Держите запас — переполнение
  диска во время сборки повреждает Docker-VM (см. [Диагностику](#диагностика-проблем)).
- Для локальной разработки вне Docker: **[uv](https://docs.astral.sh/uv/)** и
  Python 3.12.
- **Ключи API** (опциональны, но без них — деградированный режим):
  - `OPENROUTER_API_KEY` — разбор запросов и генерация объяснений (Qwen-72B).
    Без него NLU деградирует: весь текст уходит в семантический поиск.
  - `ORS_API_KEY` — точные изохроны OpenRouteService. Без него гео-доступность
    считается по кругу-радиусу (грубее, но работает).

---

## Быстрый старт (Docker)

```bash
# 1. Секреты
cp .env.example .env
# отредактируйте .env — впишите OPENROUTER_API_KEY (и, по желанию, ORS_API_KEY)

# 2. Поднять весь стек
docker compose up --build
```

Откройте **http://localhost:3000**.

> ⚠️ **Прочитайте следующий раздел перед первым запуском** — первый старт долгий,
> и на машине без GPU есть важный нюанс с производительностью поиска.

Остановить: `docker compose down` (данные в volume сохраняются).
Полностью, включая данные: `docker compose down -v`.

---

## Первый запуск: чего ожидать

1. **Сборка образов — минуты.** ML-образ (~9 ГБ, PyTorch) собирается и
   экспортируется на диск заметно долго, особенно на macOS (Docker-VM).

2. **Скачивание моделей — ~4.6 ГБ.** Одноразовый сервис `ml-model-cache`
   выкачивает BGE-M3 и bge-reranker в volume `habitus_hf`. Следующие запуски
   переиспользуют кэш. `ml-service` стартует только после этого.

   > Если модели уже лежат в хостовом `~/.cache/huggingface`, можно предзасеять
   > volume и не качать заново:
   > ```bash
   > docker volume create habitus_habitus_hf
   > docker run --rm -v habitus_habitus_hf:/dest -v ~/.cache/huggingface:/src:ro \
   >   alpine sh -c 'cp -a /src/. /dest/'
   > ```

3. **Прогрев backend.** Backend делает синхронный `/search` до открытия порта —
   на первом запросе модели грузятся в память (BGE-M3 ~87 с холодного старта).
   Пока идёт прогрев, backend «unhealthy» — это нормально.

4. **База пустая.** Свежий стек поднимается без объявлений. Чтобы искать и
   строить досье, наполните БД — см. [Наполнение данными](#наполнение-данными).

---

## Наполнение данными

Все команды выполняются к поднятой БД (порт 5544). Запускать можно как из
контейнера, так и с хоста через `uv run`.

### 1. Объявления → эмбеддинги (оффлайн-пайплайн)

```bash
# Циановский срез с реальными описаниями (рекомендуется — качественный поиск):
uv run habitus offline --csv listings.csv --source cian

# либо Kaggle-датасет (без прозы, поиск слабее — только для проводки):
uv run habitus offline --csv <kaggle.csv> --source kaggle
```

Пайплайн: загрузка CSV → очистка/геокодирование → OSM-POI + гео-обогащение
(PostGIS) → текстификация → BGE-M3 dense+sparse эмбеддинги. Инкрементальный:
повторный прогон досчитывает только новое.

### 2. Гео-слои для досье

```bash
# Контуры зданий/парков/воды из OSM (вид из окна, тени/инсоляция). Сеть, ~5 мин:
uv run habitus import-osm-features

# Слои коммунального фонда / преступности / шума. Готовый генератор из
# открытых источников (OSM + POI), пишет GeoJSON:
uv run python scripts/build_evidence.py --out data/evidence_msk.geojson
uv run habitus import-evidence --geojson data/evidence_msk.geojson
```

Без этих слоёв `/dossier` не падает — соответствующие блоки честно
деградируют в `secondary` (никаких синтетических нулей).

Формат GeoJSON для `import-evidence`: корень — `FeatureCollection`. Для каждого
`Feature` обязательны свойства `source_id`, `source`, `city: "msk"`, `layer`,
`observed_at`. Слои `communal`/`crime` — только Polygon/MultiPolygon и
`weight ∈ [0,1]`; `noise` — любая геометрия с неотрицательным `db`. Импорт
идемпотентен по `(source, source_id, layer)`.

### 3. Сторона света окон (из прозы описаний)

```bash
uv run habitus extract-windows           # весь массив
uv run habitus extract-windows --limit 50  # пробный прогон
```

LLM извлекает ориентацию окон из текста объявлений (строго явно написанное;
закат→W, рассвет→E) + детерминированный лексический гейт против галлюцинаций.
Оживляет блок «вид/инсоляция» в досье.

### Быстрый путь: готовый дамп

Если есть дамп наполненной БД (`pg_dump -Fc` пяти таблиц `raw_listings`,
`listings`, `poi`, `urban_features`, `urban_evidence`), восстановление занимает
минуты вместо часов пайплайна:

```bash
docker compose exec -T db pg_restore -U habitus -d habitus \
  --no-owner --clean --if-exists < habitus_data.dump
```

---

## Производительность и режим «нативный ML»

**Важно для машин без CUDA (в т.ч. Apple Silicon).** Кросс-энкодер реранкера
работает **на CPU** (на Apple MPS он крашится, поэтому принудительно пиннится на
CPU — см. `habitus/online/rerank.py`). На длинных описаниях это делает каждый
`/search` медленным:

- **В Docker на CPU: 1–2.5 минуты на запрос** — для интерактивного теста
  непригодно.
- Латентность складывается из двух вызовов OpenRouter (разбор запроса +
  объяснение, Qwen-72B, по 6–17 с) и CPU-реранка. **Повторный тот же запрос
  мгновенен** (кэш `parse_cache`/`explain_cache`).

Что помогает:

- **`RERANK_MAX_LENGTH`** (по умолчанию 512) — обрезка документа для реранкера.
  `128` ускоряет реранк ~в 3 раза при малой потере качества.
- **`RETRIEVAL_TOP_K`** — сколько кандидатов идёт в реранк. Меньше → быстрее.

### Гибрид: db/backend/frontend в Docker, ML — нативно

Для приемлемой скорости на Apple Silicon запускайте `ml-service` **нативно**
(BGE-M3 использует MPS для эмбеддингов), остальное — в Docker. Создайте
`docker-compose.override.yml` (он в `.gitignore`):

```yaml
services:
  backend:
    environment:
      ML_SERVICE_URL: http://host.docker.internal:8000
      ML_SEARCH_TIMEOUT_S: "120"
      ML_WARMUP_TIMEOUT_S: "180"
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

Затем:

```bash
# 1. Поднять инфраструктуру без ML-контейнера
docker compose up -d db

# 2. ML нативно на хосте (быстрый конфиг)
RETRIEVAL_TOP_K=20 RERANK_MAX_LENGTH=128 \
  uv run uvicorn habitus.online.service:app --host 0.0.0.0 --port 8000

# 3. Прогреть ML одним запросом (холодный старт грузит модели ~1.5 мин)
curl -X POST http://localhost:8000/search \
  -H "Content-Type: application/json" \
  -d '{"query":"тихая двушка рядом с метро до 40 млн"}'

# 4. Поднять backend и frontend (не тянуть ML-контейнер как зависимость)
docker compose up -d --no-deps backend
docker compose up -d --no-deps frontend
```

Даже так свежий поиск ~30–60 с (два LLM-вызова + CPU-реранк). На проде с GPU
всё это — доли секунды.

---

## Локальная разработка

```bash
uv sync                        # установить зависимости
uv run pytest                  # тесты Python
cd backend && go test ./...    # тесты Go
cd frontend && npm test        # тесты фронта
```

> ⚠️ **Не гоняйте полный `pytest` на наполненной dev-БД.** Часть тестов делает
> `TRUNCATE`/`DROP` таблиц (`test_pipeline`, `test_eval`, `test_retrieval_db`,
> `test_cli_smoke`) и затрёт данные. Для тестов используйте отдельную БД:
> ```bash
> psql -h localhost -p 5544 -U habitus -d habitus -c "CREATE DATABASE habitus_test;"
> DB_DSN="postgresql://habitus:habitus@localhost:5544/habitus_test" uv run pytest
> ```

---

## CLI-команды

```bash
uv run habitus offline --csv <path> --source {cian,kaggle}  # оффлайн-пайплайн
uv run habitus update                                        # инкрементальный досчёт
uv run habitus search "тихая двушка рядом с метро"          # поиск из терминала
uv run habitus eval                                          # метрики на golden-set
uv run habitus import-osm-features                           # контуры зданий (OSM)
uv run habitus import-evidence --geojson <path>              # слои communal/crime/noise
uv run habitus extract-windows [--limit N]                   # ориентация окон из прозы
```

---

## Переменные окружения

Задаются через `.env` (см. `.env.example`) или окружение контейнеров.

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `DB_DSN` | `postgresql://habitus:habitus@localhost:5544/habitus` | Подключение к БД |
| `OPENROUTER_API_KEY` | — | LLM (разбор запроса, объяснение, Q&A) |
| `ORS_API_KEY` | — | Изохроны OpenRouteService |
| `RETRIEVAL_TOP_K` | `100` | Кандидатов в реранк (меньше → быстрее) |
| `RERANK_MAX_LENGTH` | `512` | Обрезка документа для реранкера (128 → быстрее) |
| `EMBED_MODEL` | `BAAI/bge-m3` | Модель эмбеддингов |
| `POI_RADIUS_M` | `500` | Радиус учёта POI |
| `ML_DOSSIER_TIMEOUT_S` | `30` | Таймаут построения досье |
| `ML_OBJECT_ASK_TIMEOUT_S` | `45` | Таймаут Q&A по объекту |

Секреты **не коммитить** — `.env` в `.gitignore`. На проде передавайте ключи
через секрет-хранилище платформы.

---

## Диагностика проблем

**Backend «unhealthy», в логах `ML warm-up failed ... timeout`.**
Прогревочный `/search` не уложился в `ML_SEARCH_TIMEOUT_S` — типично на CPU.
Уменьшите `RETRIEVAL_TOP_K` и `RERANK_MAX_LENGTH`, поднимите `ML_SEARCH_TIMEOUT_S`,
или используйте [нативный режим ML](#гибрид-dbbackendfrontend-в-docker-ml--нативно).

**Frontend не стартует.** У него `depends_on: backend (healthy)` — пока backend
греется/unhealthy, frontend ждёт. Дождитесь healthy backend.

**Первый `/search` очень долгий (>1 мин) и висит.** Это не зависание — на
холодном старте грузятся модели (BGE-M3 ~87 с) и идёт CPU-реранк. Прогрейте ML
одним запросом заранее.

**Docker-демон завис, команды виснут, в логах I/O-ошибки `vda1`.**
Переполнение диска повреждает файловую систему Docker-VM. Освободите ≥ 15 ГБ на
хосте, затем:
```bash
pkill -9 -f com.docker.backend    # жёстко убить зависший Docker Desktop
open -a Docker                    # (macOS) перезапустить; на Linux — systemctl restart docker
# после подъёма демона вернуть место внутри Docker:
docker builder prune -af
docker image prune -f
```

**`docker compose up` падает с `no space left on device`.**
Кончилось место (диск хоста или Docker-VM). Освободите место, вычистите кэш
сборки (`docker builder prune -af`) и повторите.
