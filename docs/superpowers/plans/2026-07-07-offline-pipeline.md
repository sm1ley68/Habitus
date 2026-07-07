# Offline-пайплайн (Фаза 1) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Собрать offline-пайплайн ML-части: от загрузки данных недвижимости по Москве до готовой к гибридному retrieval таблицы в Postgres (структура + гео-обогащение + dense/sparse эмбеддинги), с инкрементальным обновлением по cron.

**Architecture:** Плоские Python-модули с одной ответственностью каждый, оркеструемые тонким CLI. Единое хранилище Postgres+PostGIS+pgvector (локальный docker для разработки). Данные текут: ingest → clean → geo enrich → embed → update. Своя модель не обучается — эмбеддинги это инференс готовой BGE-M3.

**Tech Stack:** Python 3.12, `uv`, Postgres 16 + PostGIS + pgvector, `psycopg` (v3), `FlagEmbedding` (BGE-M3), `pyosmium`/Overpass, `geopandas`, `pandas`, `pytest`, `docker-compose`.

## Global Constraints

- Целевой регион: **Москва** (фильтр `region == 3` в датасете mrdaniilak, плюс bbox Москвы для OSM).
- Эмбеддинги: **BAAI/bge-m3**, dense размерность **1024**, `normalize_embeddings=True`. Модель фиксирована — смена = полный реиндекс.
- Sparse-вектор pgvector тип `sparsevec`, версия pgvector **≥ 0.7**.
- Всё идемпотентно: upsert по `external_id`. Повторный прогон безопасен.
- Python **3.12**, зависимости только через `uv` (`pyproject.toml`, без прямого pip).
- Радиус плотности POI: **500 м**. Единица расстояния для гео — метры (гео-расчёты в EPSG:3857 или geography-cast; координаты хранятся в 4326).
- Секреты только из env (`.env`, не в git). Kaggle-токен, геокодинг-ключ — по запросу.
- Не трогать: прод-сервер БД, API/FastAPI-деплой, транспорт, клиент ORS/Valhalla (зона беков).

---

### Task 1: Скелет проекта (uv, конфиг, docker)

**Files:**
- Create: `pyproject.toml`
- Create: `.env.example`
- Create: `.gitignore`
- Create: `docker-compose.yml`
- Create: `habitus/__init__.py`
- Create: `habitus/config.py`
- Test: `tests/test_config.py`

**Interfaces:**
- Produces: `habitus.config.settings` — объект с полями `db_dsn: str`, `city_region_code: int`, `poi_radius_m: int`, `embed_model: str`, `embed_dim: int`, `data_dir: Path`.

- [ ] **Step 1: Написать pyproject.toml**

```toml
[project]
name = "habitus"
version = "0.1.0"
requires-python = ">=3.12"
dependencies = [
    "psycopg[binary]>=3.2",
    "pandas>=2.2",
    "pydantic-settings>=2.4",
    "python-dotenv>=1.0",
    "FlagEmbedding>=1.3",
    "geopandas>=1.0",
    "requests>=2.32",
    "kaggle>=1.6",
    "osmium>=3.7",
]

[dependency-groups]
dev = ["pytest>=8.3", "pytest-postgresql>=6.1"]

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.pytest.ini_options]
testpaths = ["tests"]
```

- [ ] **Step 2: Написать .gitignore, .env.example, docker-compose.yml**

`.gitignore`:
```
.venv/
__pycache__/
*.pyc
.env
data/
.pytest_cache/
```

`.env.example`:
```
DB_DSN=postgresql://habitus:habitus@localhost:5433/habitus
CITY_REGION_CODE=3
POI_RADIUS_M=500
EMBED_MODEL=BAAI/bge-m3
EMBED_DIM=1024
DATA_DIR=./data
KAGGLE_USERNAME=
KAGGLE_KEY=
```

`docker-compose.yml` (образ уже содержит PostGIS + pgvector):
```yaml
services:
  db:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_USER: habitus
      POSTGRES_PASSWORD: habitus
      POSTGRES_DB: habitus
    ports:
      - "5433:5432"
    volumes:
      - habitus_pg:/var/lib/postgresql/data
volumes:
  habitus_pg:
```
Примечание: образ `pgvector/pgvector:pg16` включает pgvector, но **не** PostGIS. Task 2 установит PostGIS через `CREATE EXTENSION` — базовый образ `pg16` в этом теге содержит нужные библиотеки contrib только если это `pgvector`-сборка на debian. Если `CREATE EXTENSION postgis` упадёт, заменить образ на `ghcr.io/baosystems/postgis:16` + отдельная установка pgvector; проверяется в Task 2 Step 3.

- [ ] **Step 3: Написать failing-тест конфига**

```python
# tests/test_config.py
from habitus.config import settings

def test_settings_defaults():
    assert settings.embed_dim == 1024
    assert settings.embed_model == "BAAI/bge-m3"
    assert settings.poi_radius_m == 500
    assert settings.city_region_code == 3
    assert "postgresql://" in settings.db_dsn
```

- [ ] **Step 4: Запустить тест — убедиться, что падает**

Run: `uv run pytest tests/test_config.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.config`)

- [ ] **Step 5: Реализовать config.py**

```python
# habitus/config.py
from pathlib import Path
from pydantic_settings import BaseSettings, SettingsConfigDict

class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    db_dsn: str = "postgresql://habitus:habitus@localhost:5433/habitus"
    city_region_code: int = 3
    poi_radius_m: int = 500
    embed_model: str = "BAAI/bge-m3"
    embed_dim: int = 1024
    data_dir: Path = Path("./data")
    kaggle_username: str = ""
    kaggle_key: str = ""

settings = Settings()
```

Также создать пустой `habitus/__init__.py`.

- [ ] **Step 6: Запустить тест — PASS**

Run: `uv run pytest tests/test_config.py -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pyproject.toml .gitignore .env.example docker-compose.yml habitus/ tests/test_config.py
git commit -m "chore: скелет проекта, конфиг и docker-compose (Postgres+PostGIS+pgvector)"
```

---

### Task 2: Схема БД и коннект-хелпер

**Files:**
- Create: `habitus/db/__init__.py`
- Create: `habitus/db/schema.sql`
- Create: `habitus/db/connection.py`
- Create: `habitus/db/init_db.py`
- Test: `tests/test_schema.py`

**Interfaces:**
- Consumes: `habitus.config.settings.db_dsn`.
- Produces:
  - `habitus.db.connection.get_conn() -> psycopg.Connection` (контекст-менеджер-совместимое соединение).
  - `habitus.db.init_db.init_db(conn) -> None` — применяет `schema.sql` идемпотентно.
  - Таблицы `listings`, `poi` с колонками из спеки (раздел 4).

- [ ] **Step 1: Написать schema.sql**

```sql
-- habitus/db/schema.sql
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS listings (
    id                 BIGSERIAL PRIMARY KEY,
    external_id        TEXT UNIQUE NOT NULL,
    source             TEXT NOT NULL,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    price              BIGINT,
    area               REAL,
    kitchen_area       REAL,
    rooms              INTEGER,
    level              INTEGER,
    levels             INTEGER,
    building_type      INTEGER,
    object_type        INTEGER,
    geom               geometry(Point, 4326),
    walk_min_school    REAL,
    walk_min_metro     REAL,
    walk_min_park      REAL,
    bar_density_500m   INTEGER,
    window_orientation TEXT[],
    insolation_rough   REAL,
    noise_level        TEXT,
    description        TEXT,
    doc_text           TEXT,
    embedding          vector(1024),
    sparse_embedding   sparsevec(250002),
    content_hash       TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS poi (
    id         BIGSERIAL PRIMARY KEY,
    osm_id     BIGINT,
    kind       TEXT NOT NULL,
    name       TEXT,
    rating     REAL,
    geom       geometry(Point, 4326),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (osm_id, kind)
);

CREATE INDEX IF NOT EXISTS listings_geom_gix ON listings USING GIST (geom);
CREATE INDEX IF NOT EXISTS listings_price_ix ON listings (price);
CREATE INDEX IF NOT EXISTS listings_embedding_hnsw
    ON listings USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS poi_geom_gix ON poi USING GIST (geom);
CREATE INDEX IF NOT EXISTS poi_kind_ix ON poi (kind);
```
Примечание: `sparsevec(250002)` — размер словаря BGE-M3 (XLM-RoBERTa vocab). Значение проверить в Task 9 и при расхождении поправить в один прогон миграции (реиндекс sparse дёшев, он не HNSW).

- [ ] **Step 2: Реализовать connection.py и init_db.py**

```python
# habitus/db/connection.py
import psycopg
from habitus.config import settings

def get_conn() -> psycopg.Connection:
    return psycopg.connect(settings.db_dsn)
```

```python
# habitus/db/init_db.py
from pathlib import Path
import psycopg

SCHEMA_PATH = Path(__file__).parent / "schema.sql"

def init_db(conn: psycopg.Connection) -> None:
    sql = SCHEMA_PATH.read_text(encoding="utf-8")
    with conn.cursor() as cur:
        cur.execute(sql)
    conn.commit()
```

- [ ] **Step 3: Написать тест схемы (требует запущенный docker)**

```python
# tests/test_schema.py
import psycopg
from habitus.db.init_db import init_db
from habitus.config import settings

def test_init_db_creates_tables():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT to_regclass('listings'), to_regclass('poi');")
            listings, poi = cur.fetchone()
        assert listings == "listings"
        assert poi == "poi"

def test_extensions_present():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("SELECT extname FROM pg_extension;")
            names = {r[0] for r in cur.fetchall()}
        assert "postgis" in names
        assert "vector" in names
```

- [ ] **Step 4: Поднять docker и прогнать тест**

Run:
```bash
docker compose up -d
uv run pytest tests/test_schema.py -v
```
Expected: PASS. Если `CREATE EXTENSION postgis` падает — сменить образ в `docker-compose.yml` на PostGIS-образ с pgvector (см. примечание Task 1 Step 2), пересоздать том, повторить.

- [ ] **Step 5: Commit**

```bash
git add habitus/db/ tests/test_schema.py
git commit -m "feat: схема БД (listings, poi) + init_db, коннект-хелпер"
```

---

### Task 3: Загрузчик Kaggle-датасета → raw_listings

**Files:**
- Create: `habitus/ingest/__init__.py`
- Create: `habitus/ingest/kaggle_loader.py`
- Modify: `habitus/db/schema.sql` (добавить `raw_listings`)
- Test: `tests/test_kaggle_loader.py`
- Test: `tests/fixtures/sample_russia_realestate.csv`

**Interfaces:**
- Consumes: `settings.data_dir`, `settings.city_region_code`, `get_conn`.
- Produces:
  - `habitus.ingest.kaggle_loader.parse_csv(path: Path) -> list[dict]` — читает CSV, фильтрует по региону Москвы, маппит колонки в наш словарь с ключами `external_id, source, price, area, kitchen_area, rooms, level, levels, building_type, object_type, lat, lon`.
  - `habitus.ingest.kaggle_loader.load_to_raw(rows: list[dict], conn) -> int` — upsert в `raw_listings`, возвращает число строк.

- [ ] **Step 1: Добавить raw_listings в schema.sql**

Добавить в конец `habitus/db/schema.sql`:
```sql
CREATE TABLE IF NOT EXISTS raw_listings (
    external_id   TEXT PRIMARY KEY,
    source        TEXT NOT NULL,
    price         BIGINT,
    area          REAL,
    kitchen_area  REAL,
    rooms         INTEGER,
    level         INTEGER,
    levels        INTEGER,
    building_type INTEGER,
    object_type   INTEGER,
    lat           DOUBLE PRECISION,
    lon           DOUBLE PRECISION,
    description   TEXT,
    ingested_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Создать фикстуру CSV**

Датасет mrdaniilak имеет колонки: `date,time,geo_lat,geo_lon,region,building_type,level,levels,rooms,area,kitchen_area,object_type,price`. Создать `tests/fixtures/sample_russia_realestate.csv`:
```csv
date,time,geo_lat,geo_lon,region,building_type,level,levels,rooms,area,kitchen_area,object_type,price
2021-01-01,12:00:00,55.7558,37.6173,3,1,7,12,2,54.0,9.0,1,12000000
2021-01-02,13:00:00,59.9311,30.3609,2,2,3,5,1,33.0,7.0,1,7000000
2021-01-03,10:00:00,55.7601,37.6200,3,4,1,9,3,72.0,11.0,2,18000000
```
(вторая строка — Питер, регион 2 — должна отфильтроваться).

- [ ] **Step 3: Написать failing-тесты**

```python
# tests/test_kaggle_loader.py
from pathlib import Path
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
from habitus.db.init_db import init_db
import psycopg
from habitus.config import settings

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"

def test_parse_filters_moscow_only():
    rows = parse_csv(FIX)
    assert len(rows) == 2  # питерская строка отфильтрована
    assert all(r["source"] == "kaggle" for r in rows)
    first = rows[0]
    assert first["rooms"] == 2
    assert first["price"] == 12000000
    assert abs(first["lat"] - 55.7558) < 1e-4
    assert first["external_id"]  # непустой стабильный id

def test_parse_stable_external_id():
    rows1 = parse_csv(FIX)
    rows2 = parse_csv(FIX)
    assert rows1[0]["external_id"] == rows2[0]["external_id"]

def test_load_to_raw_upsert_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        rows = parse_csv(FIX)
        n1 = load_to_raw(rows, conn)
        n2 = load_to_raw(rows, conn)  # повтор
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM raw_listings WHERE source='kaggle';")
            total = cur.fetchone()[0]
        assert n1 == 2
        assert total == 2  # не задвоилось
```

- [ ] **Step 4: Запустить — FAIL**

Run: `uv run pytest tests/test_kaggle_loader.py -v`
Expected: FAIL (`ModuleNotFoundError`)

- [ ] **Step 5: Реализовать kaggle_loader.py**

```python
# habitus/ingest/kaggle_loader.py
import csv
import hashlib
from pathlib import Path
import psycopg
from habitus.config import settings

def _external_id(row: dict) -> str:
    key = f"{row['geo_lat']}|{row['geo_lon']}|{row['area']}|{row['price']}|{row['date']}"
    return "kaggle_" + hashlib.sha1(key.encode()).hexdigest()[:16]

def _to_int(v):
    try: return int(float(v))
    except (ValueError, TypeError): return None

def _to_float(v):
    try: return float(v)
    except (ValueError, TypeError): return None

def parse_csv(path: Path) -> list[dict]:
    out = []
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            if _to_int(row.get("region")) != settings.city_region_code:
                continue
            out.append({
                "external_id": _external_id(row),
                "source": "kaggle",
                "price": _to_int(row.get("price")),
                "area": _to_float(row.get("area")),
                "kitchen_area": _to_float(row.get("kitchen_area")),
                "rooms": _to_int(row.get("rooms")),
                "level": _to_int(row.get("level")),
                "levels": _to_int(row.get("levels")),
                "building_type": _to_int(row.get("building_type")),
                "object_type": _to_int(row.get("object_type")),
                "lat": _to_float(row.get("geo_lat")),
                "lon": _to_float(row.get("geo_lon")),
                "description": None,
            })
    return out

def load_to_raw(rows: list[dict], conn: psycopg.Connection) -> int:
    cols = ["external_id","source","price","area","kitchen_area","rooms",
            "level","levels","building_type","object_type","lat","lon","description"]
    sql = f"""
        INSERT INTO raw_listings ({",".join(cols)})
        VALUES ({",".join("%("+c+")s" for c in cols)})
        ON CONFLICT (external_id) DO UPDATE SET
            price=EXCLUDED.price, area=EXCLUDED.area, description=EXCLUDED.description,
            ingested_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, rows)
    conn.commit()
    return len(rows)
```

- [ ] **Step 6: Запустить — PASS**

Run: `uv run pytest tests/test_kaggle_loader.py -v`
Expected: PASS

- [ ] **Step 7: Добавить CLI-обёртку скачивания (опционально при наличии токена)**

В `kaggle_loader.py` добавить:
```python
def download_dataset() -> Path:
    """Скачивает mrdaniilak/russia-real-estate-20182021 в data_dir. Требует KAGGLE_KEY."""
    import kaggle
    dest = settings.data_dir / "kaggle"
    dest.mkdir(parents=True, exist_ok=True)
    kaggle.api.dataset_download_files(
        "mrdaniilak/russia-real-estate-20182021", path=str(dest), unzip=True)
    csvs = list(dest.glob("*.csv"))
    if not csvs:
        raise FileNotFoundError("CSV не найден после распаковки Kaggle-датасета")
    return max(csvs, key=lambda p: p.stat().st_size)
```
(Не тестируется в CI — требует внешний токен. Запросить `KAGGLE_KEY` у пользователя при запуске.)

- [ ] **Step 8: Commit**

```bash
git add habitus/ingest/ habitus/db/schema.sql tests/test_kaggle_loader.py tests/fixtures/
git commit -m "feat: Kaggle-загрузчик → raw_listings (фильтр Москвы, идемпотентный upsert)"
```

---

### Task 4: Очистка и нормализация → listings

**Files:**
- Create: `habitus/clean/__init__.py`
- Create: `habitus/clean/normalize.py`
- Test: `tests/test_normalize.py`

**Interfaces:**
- Consumes: таблица `raw_listings`, `get_conn`.
- Produces:
  - `habitus.clean.normalize.is_valid(row: dict) -> bool` — отсев мусора (цена/площадь в разумных пределах, координаты в bbox Москвы).
  - `habitus.clean.normalize.promote_to_listings(conn) -> int` — переносит валидные raw-строки в `listings` (upsert по external_id, geom из lat/lon), возвращает число.

- [ ] **Step 1: Написать failing-тесты**

```python
# tests/test_normalize.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
from habitus.clean.normalize import is_valid, promote_to_listings
from pathlib import Path

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"

def test_is_valid_rejects_garbage():
    assert is_valid({"price": 12000000, "area": 54.0, "lat": 55.75, "lon": 37.61})
    assert not is_valid({"price": 0, "area": 54.0, "lat": 55.75, "lon": 37.61})
    assert not is_valid({"price": 12000000, "area": 54.0, "lat": 0.0, "lon": 0.0})
    assert not is_valid({"price": 12000000, "area": 2.0, "lat": 55.75, "lon": 37.61})

def test_promote_sets_geom_and_is_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE raw_listings, listings;")
        conn.commit()
        load_to_raw(parse_csv(FIX), conn)
        n1 = promote_to_listings(conn)
        n2 = promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT count(*), count(geom) FROM listings;")
            total, with_geom = cur.fetchone()
        assert n1 == 2
        assert total == 2 and with_geom == 2
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_normalize.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать normalize.py**

```python
# habitus/clean/normalize.py
import psycopg

# грубый bbox Москвы (в пределах МКАД + Новая Москва небольшим запасом)
MSK_BBOX = (37.30, 55.48, 37.95, 55.95)  # lon_min, lat_min, lon_max, lat_max

def is_valid(row: dict) -> bool:
    price = row.get("price") or 0
    area = row.get("area") or 0
    lat, lon = row.get("lat"), row.get("lon")
    if not (1_000_000 <= price <= 3_000_000_000):
        return False
    if not (5 <= area <= 1000):
        return False
    if lat is None or lon is None:
        return False
    lon_min, lat_min, lon_max, lat_max = MSK_BBOX
    if not (lon_min <= lon <= lon_max and lat_min <= lat <= lat_max):
        return False
    return True

def promote_to_listings(conn: psycopg.Connection) -> int:
    with conn.cursor(row_factory=psycopg.rows.dict_row) as cur:
        cur.execute("SELECT * FROM raw_listings;")
        raws = cur.fetchall()
    valid = [r for r in raws if is_valid(r)]
    sql = """
        INSERT INTO listings
          (external_id, source, price, area, kitchen_area, rooms, level, levels,
           building_type, object_type, geom, description)
        VALUES
          (%(external_id)s, %(source)s, %(price)s, %(area)s, %(kitchen_area)s,
           %(rooms)s, %(level)s, %(levels)s, %(building_type)s, %(object_type)s,
           ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326), %(description)s)
        ON CONFLICT (external_id) DO UPDATE SET
           price=EXCLUDED.price, area=EXCLUDED.area, geom=EXCLUDED.geom,
           description=EXCLUDED.description, updated_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, valid)
    conn.commit()
    return len(valid)
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_normalize.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/clean/ tests/test_normalize.py
git commit -m "feat: очистка/нормализация raw_listings → listings (отсев мусора, geom, upsert)"
```

---

### Task 5: Геокодинг строк без координат

**Files:**
- Create: `habitus/clean/geocode.py`
- Test: `tests/test_geocode.py`

**Interfaces:**
- Consumes: `listings` с `geom IS NULL` и непустым `description`/адресом (для скрапленных строк без координат).
- Produces:
  - `habitus.clean.geocode.geocode_address(addr: str, session=None) -> tuple[float,float] | None` — (lon, lat) через Nominatim, ретраи+кэш.
  - `habitus.clean.geocode.backfill_missing_coords(conn, geocoder=geocode_address) -> int` — заполняет `geom` там, где NULL; `geocoder` инъектируется для тестов.

Примечание: Kaggle-строки уже имеют координаты, геокодинг нужен в основном для скрапера. Функция готова заранее.

- [ ] **Step 1: Написать failing-тесты (с моком геокодера)**

```python
# tests/test_geocode.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.clean.geocode import backfill_missing_coords

def test_backfill_uses_injected_geocoder():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, description)
                           VALUES ('t1','cian','ул. Тверская, 1');""")
        conn.commit()
        fake = lambda addr, session=None: (37.6113, 55.7570)
        n = backfill_missing_coords(conn, geocoder=fake)
        with conn.cursor() as cur:
            cur.execute("SELECT ST_X(geom), ST_Y(geom) FROM listings WHERE external_id='t1';")
            x, y = cur.fetchone()
        assert n == 1
        assert abs(x - 37.6113) < 1e-4 and abs(y - 55.7570) < 1e-4
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_geocode.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать geocode.py**

```python
# habitus/clean/geocode.py
import time
from functools import lru_cache
import requests
import psycopg

NOMINATIM = "https://nominatim.openstreetmap.org/search"

@lru_cache(maxsize=10000)
def geocode_address(addr: str, session=None) -> tuple[float, float] | None:
    for attempt in range(3):
        try:
            r = requests.get(
                NOMINATIM,
                params={"q": addr, "format": "json", "limit": 1, "countrycodes": "ru"},
                headers={"User-Agent": "habitus-ml/0.1"},
                timeout=10,
            )
            r.raise_for_status()
            data = r.json()
            if not data:
                return None
            time.sleep(1.0)  # уважение лимита Nominatim (1 req/s)
            return (float(data[0]["lon"]), float(data[0]["lat"]))
        except (requests.RequestException, ValueError, KeyError):
            time.sleep(2 ** attempt)
    return None

def backfill_missing_coords(conn: psycopg.Connection, geocoder=geocode_address) -> int:
    with conn.cursor() as cur:
        cur.execute("""SELECT external_id, description FROM listings
                       WHERE geom IS NULL AND description IS NOT NULL;""")
        rows = cur.fetchall()
    updated = 0
    for ext_id, addr in rows:
        res = geocoder(addr)
        if res is None:
            continue
        lon, lat = res
        with conn.cursor() as cur:
            cur.execute("""UPDATE listings
                           SET geom=ST_SetSRID(ST_MakePoint(%s,%s),4326), updated_at=now()
                           WHERE external_id=%s;""", (lon, lat, ext_id))
        updated += 1
    conn.commit()
    return updated
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_geocode.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/clean/geocode.py tests/test_geocode.py
git commit -m "feat: геокодинг строк без координат (Nominatim, ретраи+кэш, инъекция для тестов)"
```

---

### Task 6: Выгрузка OSM POI Москвы → poi

**Files:**
- Create: `habitus/geo/__init__.py`
- Create: `habitus/geo/osm_extract.py`
- Test: `tests/test_osm_extract.py`

**Interfaces:**
- Consumes: `get_conn`.
- Produces:
  - `habitus.geo.osm_extract.OVERPASS_QUERIES: dict[str, str]` — kind → Overpass QL.
  - `habitus.geo.osm_extract.parse_overpass(kind: str, payload: dict) -> list[dict]` — элементы Overpass JSON → строки `{osm_id, kind, name, lat, lon}`.
  - `habitus.geo.osm_extract.upsert_poi(rows: list[dict], conn) -> int` — upsert по (osm_id, kind).
  - `habitus.geo.osm_extract.fetch_kind(kind, http_get=requests.get) -> list[dict]` — качает Overpass; `http_get` инъектируется.

- [ ] **Step 1: Написать failing-тесты (парсинг+upsert, без сети)**

```python
# tests/test_osm_extract.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.osm_extract import parse_overpass, upsert_poi

SAMPLE = {"elements": [
    {"type": "node", "id": 111, "lat": 55.76, "lon": 37.62, "tags": {"name": "Бар А"}},
    {"type": "node", "id": 222, "lat": 55.77, "lon": 37.63, "tags": {}},
]}

def test_parse_overpass_maps_fields():
    rows = parse_overpass("bar", SAMPLE)
    assert len(rows) == 2
    assert rows[0] == {"osm_id": 111, "kind": "bar", "name": "Бар А",
                       "lat": 55.76, "lon": 37.62}
    assert rows[1]["name"] is None

def test_upsert_poi_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE poi;")
        conn.commit()
        rows = parse_overpass("bar", SAMPLE)
        upsert_poi(rows, conn)
        upsert_poi(rows, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT count(*), count(geom) FROM poi WHERE kind='bar';")
            total, with_geom = cur.fetchone()
        assert total == 2 and with_geom == 2
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_osm_extract.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать osm_extract.py**

```python
# habitus/geo/osm_extract.py
import requests
import psycopg

OVERPASS_URL = "https://overpass-api.de/api/interpreter"
MSK_AREA = "(55.48,37.30,55.95,37.95)"  # bbox: south,west,north,east

OVERPASS_QUERIES = {
    "school":     f'node["amenity"="school"]{MSK_AREA};',
    "bar":        f'node["amenity"~"bar|pub"]{MSK_AREA};',
    "alcohol":    f'node["shop"="alcohol"]{MSK_AREA};',
    "park":       f'node["leisure"="park"]{MSK_AREA};',
    "metro":      f'node["station"="subway"]{MSK_AREA};',
}

def parse_overpass(kind: str, payload: dict) -> list[dict]:
    rows = []
    for el in payload.get("elements", []):
        if el.get("type") != "node":
            continue
        rows.append({
            "osm_id": el["id"],
            "kind": kind,
            "name": el.get("tags", {}).get("name"),
            "lat": el["lat"],
            "lon": el["lon"],
        })
    return rows

def fetch_kind(kind: str, http_get=requests.get) -> list[dict]:
    q = f"[out:json][timeout:60];{OVERPASS_QUERIES[kind]}out;"
    r = http_get(OVERPASS_URL, params={"data": q}, timeout=90)
    r.raise_for_status()
    return parse_overpass(kind, r.json())

def upsert_poi(rows: list[dict], conn: psycopg.Connection) -> int:
    sql = """
        INSERT INTO poi (osm_id, kind, name, geom)
        VALUES (%(osm_id)s, %(kind)s, %(name)s,
                ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326))
        ON CONFLICT (osm_id, kind) DO UPDATE SET
            name=EXCLUDED.name, geom=EXCLUDED.geom, updated_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, rows)
    conn.commit()
    return len(rows)
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_osm_extract.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/geo/ tests/test_osm_extract.py
git commit -m "feat: выгрузка OSM POI Москвы (Overpass) → poi, upsert по osm_id"
```

---

### Task 7: Гео-обогащение (PostGIS производные колонки)

**Files:**
- Create: `habitus/geo/enrich.py`
- Test: `tests/test_enrich.py`

**Interfaces:**
- Consumes: `listings.geom`, `poi.geom`, `settings.poi_radius_m`.
- Produces:
  - `habitus.geo.enrich.enrich_all(conn) -> int` — считает `bar_density_500m`, `walk_min_school`/`walk_min_metro`/`walk_min_park` (прокси: расстояние/скорость пешехода), `noise_level`; пишет в колонки. Возвращает число обновлённых.
  - `habitus.geo.enrich.enrich_around(conn, poi_geom_wkt: str) -> int` — пересчёт только затронутых listings в радиусе (для инкрементала).

Прокси пешей доступности: расстояние по прямой (geography) / 1.33 м/с → минуты. Точные изохроны ORS — online-фаза, зона беков.

- [ ] **Step 1: Написать failing-тест**

```python
# tests/test_enrich.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.enrich import enrich_all

def _seed(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, poi;")
        # квартира в центре
        cur.execute("""INSERT INTO listings (external_id, source, geom)
            VALUES ('L1','kaggle', ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326));""")
        # два бара в ~200м и школа в ~300м
        cur.execute("""INSERT INTO poi (osm_id, kind, geom) VALUES
            (1,'bar', ST_SetSRID(ST_MakePoint(37.6195,55.7560),4326)),
            (2,'bar', ST_SetSRID(ST_MakePoint(37.6150,55.7550),4326)),
            (3,'school', ST_SetSRID(ST_MakePoint(37.6210,55.7560),4326));""")
    conn.commit()

def test_enrich_all_computes_density_and_walk():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn); _seed(conn)
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT bar_density_500m, walk_min_school
                           FROM listings WHERE external_id='L1';""")
            density, walk_school = cur.fetchone()
        assert density == 2          # оба бара в 500м
        assert 0 < walk_school < 15  # школа близко, разумные минуты
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_enrich.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать enrich.py**

```python
# habitus/geo/enrich.py
import psycopg
from habitus.config import settings

WALK_SPEED_MPS = 1.33  # средняя пешая скорость

_ENRICH_SQL = f"""
UPDATE listings l SET
  bar_density_500m = (
    SELECT count(*) FROM poi p
    WHERE p.kind IN ('bar','alcohol')
      AND ST_DWithin(l.geom::geography, p.geom::geography, {settings.poi_radius_m})
  ),
  walk_min_school = (
    SELECT MIN(ST_Distance(l.geom::geography, p.geom::geography)) / {WALK_SPEED_MPS} / 60.0
    FROM poi p WHERE p.kind='school'
  ),
  walk_min_metro = (
    SELECT MIN(ST_Distance(l.geom::geography, p.geom::geography)) / {WALK_SPEED_MPS} / 60.0
    FROM poi p WHERE p.kind='metro'
  ),
  walk_min_park = (
    SELECT MIN(ST_Distance(l.geom::geography, p.geom::geography)) / {WALK_SPEED_MPS} / 60.0
    FROM poi p WHERE p.kind='park'
  ),
  noise_level = CASE
    WHEN (SELECT count(*) FROM poi p WHERE p.kind='bar'
          AND ST_DWithin(l.geom::geography, p.geom::geography, 200)) > 2 THEN 'high'
    ELSE 'low' END,
  updated_at = now()
WHERE {{where}} l.geom IS NOT NULL;
"""

def enrich_all(conn: psycopg.Connection) -> int:
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL.format(where=""))
        n = cur.rowcount
    conn.commit()
    return n

def enrich_around(conn: psycopg.Connection, poi_geom_wkt: str) -> int:
    where = (f"ST_DWithin(l.geom::geography, "
             f"ST_GeogFromText('SRID=4326;{poi_geom_wkt}'), {settings.poi_radius_m}) AND ")
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL.format(where=where))
        n = cur.rowcount
    conn.commit()
    return n
```
Примечание по `window_orientation`/`insolation_rough`: требуют геометрий зданий (OSM `building`) и азимутов — вынесены в отдельную будущую задачу (см. спеку р.5); в MVP колонки остаются NULL, retrieval их не требует. Не блокирует пайплайн.

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_enrich.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/geo/enrich.py tests/test_enrich.py
git commit -m "feat: гео-обогащение PostGIS (bar_density_500m, пешая доступность, шум)"
```

---

### Task 8: Text-ification — каноничный документ под эмбеддинг

**Files:**
- Create: `habitus/embed/__init__.py`
- Create: `habitus/embed/document.py`
- Test: `tests/test_document.py`

**Interfaces:**
- Consumes: строка `listings` (dict с полями).
- Produces:
  - `habitus.embed.document.build_doc_text(row: dict) -> str` — структурные + гео-поля + (если есть) описание → каноничный русский текст.
  - `habitus.embed.document.content_hash(text: str) -> str` — sha1 для инкрементального реиндекса.

- [ ] **Step 1: Написать golden-тест**

```python
# tests/test_document.py
from habitus.embed.document import build_doc_text, content_hash

def test_build_doc_text_mute_row():
    row = {"rooms": 2, "area": 54.0, "level": 7, "levels": 12,
           "window_orientation": None, "walk_min_school": 11.5,
           "bar_density_500m": 0, "noise_level": "low", "description": None}
    text = build_doc_text(row)
    assert "2-комн" in text
    assert "54" in text
    assert "7/12" in text
    assert "школ" in text.lower()
    assert "баров" in text.lower() or "бар" in text.lower()

def test_build_doc_text_prepends_real_description():
    row = {"rooms": 1, "area": 33.0, "level": 3, "levels": 5,
           "description": "Уютная квартира, тихий двор-колодец",
           "walk_min_school": None, "bar_density_500m": 5, "noise_level": "high"}
    text = build_doc_text(row)
    assert "двор-колодец" in text
    assert "1-комн" in text

def test_content_hash_stable():
    assert content_hash("abc") == content_hash("abc")
    assert content_hash("abc") != content_hash("abd")
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_document.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать document.py**

```python
# habitus/embed/document.py
import hashlib

def _plural_rooms(n) -> str:
    return f"{n}-комн" if n else "студия/н.д."

def build_doc_text(row: dict) -> str:
    parts = []
    if row.get("description"):
        parts.append(row["description"].strip())
    parts.append(_plural_rooms(row.get("rooms")))
    if row.get("area"):
        parts.append(f"{row['area']:.0f} м²")
    if row.get("level") and row.get("levels"):
        parts.append(f"{row['level']}/{row['levels']} этаж")
    wo = row.get("window_orientation")
    if wo:
        parts.append("окна " + "/".join(wo))
    ws = row.get("walk_min_school")
    if ws is not None:
        parts.append(f"школа в {ws:.0f} мин пешком")
    bd = row.get("bar_density_500m")
    if bd is not None:
        parts.append("баров в 500 м нет" if bd == 0 else f"баров рядом: {bd}")
    if row.get("noise_level"):
        parts.append("тихо" if row["noise_level"] == "low" else "шумно")
    return ", ".join(parts)

def content_hash(text: str) -> str:
    return hashlib.sha1(text.encode("utf-8")).hexdigest()
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_document.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/embed/ tests/test_document.py
git commit -m "feat: text-ification listings → каноничный doc_text + content_hash"
```

---

### Task 9: Генерация эмбеддингов BGE-M3 (dense + sparse)

**Files:**
- Create: `habitus/embed/encode.py`
- Test: `tests/test_encode.py`

**Interfaces:**
- Consumes: `listings.doc_text`, `content_hash`, `settings.embed_model/embed_dim`.
- Produces:
  - `habitus.embed.encode.encode_texts(texts: list[str], model=None) -> list[dict]` — каждый `{"dense": list[float], "sparse": dict[int,float]}` из одного прохода BGE-M3.
  - `habitus.embed.encode.to_sparsevec_literal(sparse: dict[int,float], dim: int) -> str` — pgvector `sparsevec` литерал `"{idx:val,...}/dim"`.
  - `habitus.embed.encode.embed_pending(conn, model=None) -> int` — берёт строки где `doc_text` изменился (hash не совпал) и пишет `embedding`/`sparse_embedding`/`content_hash`. Возвращает число.

- [ ] **Step 1: Написать тесты (модель мокается, БД реальная)**

```python
# tests/test_encode.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import to_sparsevec_literal, embed_pending

def test_to_sparsevec_literal():
    lit = to_sparsevec_literal({5: 0.7, 100: 0.3}, dim=250002)
    assert lit == "{5:0.7,100:0.3}/250002"

class FakeModel:
    def encode(self, texts, **kw):
        return {
            "dense_vecs": [[0.1] * settings.embed_dim for _ in texts],
            "lexical_weights": [{"5": 0.7, "100": 0.3} for _ in texts],
        }

def test_embed_pending_only_changed():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, doc_text)
                           VALUES ('E1','kaggle','2-комн, тихо');""")
        conn.commit()
        n1 = embed_pending(conn, model=FakeModel())
        n2 = embed_pending(conn, model=FakeModel())  # hash совпал → 0
        with conn.cursor() as cur:
            cur.execute("SELECT embedding IS NOT NULL, sparse_embedding IS NOT NULL "
                        "FROM listings WHERE external_id='E1';")
            has_dense, has_sparse = cur.fetchone()
        assert n1 == 1 and n2 == 0
        assert has_dense and has_sparse
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_encode.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать encode.py**

```python
# habitus/embed/encode.py
import psycopg
from habitus.config import settings
from habitus.embed.document import content_hash

_model = None

def get_model():
    global _model
    if _model is None:
        from FlagEmbedding import BGEM3FlagModel
        _model = BGEM3FlagModel(settings.embed_model, use_fp16=True)
    return _model

def encode_texts(texts: list[str], model=None) -> list[dict]:
    m = model or get_model()
    out = m.encode(texts, return_dense=True, return_sparse=True,
                   return_colbert_vecs=False)
    results = []
    for dense, lex in zip(out["dense_vecs"], out["lexical_weights"]):
        sparse = {int(k): float(v) for k, v in lex.items()}
        results.append({"dense": list(map(float, dense)), "sparse": sparse})
    return results

def to_sparsevec_literal(sparse: dict[int, float], dim: int) -> str:
    if not sparse:
        return f"{{}}/{dim}"
    items = ",".join(f"{k}:{v}" for k, v in sorted(sparse.items()))
    return f"{{{items}}}/{dim}"

# размер словаря BGE-M3 (XLM-RoBERTa). Проверить print(len(tokenizer)) при первом прогоне.
SPARSE_DIM = 250002

def embed_pending(conn: psycopg.Connection, model=None) -> int:
    # берём все строки с doc_text и их сохранённый хэш; изменившиеся — те,
    # у кого hash(doc_text) != content_hash (в т.ч. NULL при первом прогоне).
    with conn.cursor() as cur:
        cur.execute("""SELECT external_id, doc_text, content_hash FROM listings
                       WHERE doc_text IS NOT NULL;""")
        rows = cur.fetchall()
    to_do = [(eid, txt) for eid, txt, stored in rows
             if stored != content_hash(txt)]
    if not to_do:
        return 0
    encoded = encode_texts([t for _, t in to_do], model=model)
    with conn.cursor() as cur:
        for (eid, txt), emb in zip(to_do, encoded):
            cur.execute(
                """UPDATE listings SET embedding=%s, sparse_embedding=%s::sparsevec,
                          content_hash=%s, updated_at=now() WHERE external_id=%s;""",
                (emb["dense"], to_sparsevec_literal(emb["sparse"], SPARSE_DIM),
                 content_hash(txt), eid))
    conn.commit()
    return len(to_do)
```
Примечание: упрощённая выборка pending (SELECT всех с doc_text, фильтр по хэшу в Python) корректна и достаточна на объёме MVP; при масштабировании заменить на индекс по content_hash. `SPARSE_DIM` сверить с `sparsevec(...)` в schema.sql — числа должны совпасть.

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_encode.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/embed/encode.py tests/test_encode.py
git commit -m "feat: BGE-M3 эмбеддинги dense+sparse, реиндекс только по изменившемуся content_hash"
```

---

### Task 10: Инкрементальное обновление (upsert + каскадный пересчёт)

**Files:**
- Create: `habitus/update/__init__.py`
- Create: `habitus/update/incremental.py`
- Test: `tests/test_incremental.py`

**Interfaces:**
- Consumes: `poi`, `listings`, `enrich_around`, `upsert_poi`.
- Produces:
  - `habitus.update.incremental.apply_new_poi(rows: list[dict], conn) -> int` — upsert POI + каскадный `enrich_around` по каждому новому POI. Возвращает число затронутых listings.
  - `habitus.update.incremental.deactivate_missing(active_ids: set[str], conn) -> int` — `is_active=false` для отсутствующих в свежей выдаче.

- [ ] **Step 1: Написать failing-тест**

```python
# tests/test_incremental.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.update.incremental import apply_new_poi, deactivate_missing

def test_new_bar_recomputes_nearby_density():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO listings (external_id, source, geom, bar_density_500m)
                VALUES ('L1','kaggle',
                    ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326), 0);""")
        conn.commit()
        new_bar = [{"osm_id": 999, "kind": "bar", "name": "Новый бар",
                    "lat": 55.7560, "lon": 37.6180}]
        affected = apply_new_poi(new_bar, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT bar_density_500m FROM listings WHERE external_id='L1';")
            density = cur.fetchone()[0]
        assert affected >= 1
        assert density == 1  # пересчиталось

def test_deactivate_missing():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, is_active)
                           VALUES ('A','cian',true),('B','cian',true);""")
        conn.commit()
        n = deactivate_missing({"A"}, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='B';")
            assert cur.fetchone()[0] is False
        assert n == 1
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_incremental.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать incremental.py**

```python
# habitus/update/incremental.py
import psycopg
from habitus.geo.osm_extract import upsert_poi
from habitus.geo.enrich import enrich_around

def apply_new_poi(rows: list[dict], conn: psycopg.Connection) -> int:
    upsert_poi(rows, conn)
    affected = 0
    for r in rows:
        wkt = f"POINT({r['lon']} {r['lat']})"
        affected += enrich_around(conn, wkt)
    return affected

def deactivate_missing(active_ids: set[str], conn: psycopg.Connection) -> int:
    with conn.cursor() as cur:
        cur.execute("SELECT external_id FROM listings WHERE is_active=true;")
        current = {r[0] for r in cur.fetchall()}
        missing = current - active_ids
        if missing:
            cur.execute("UPDATE listings SET is_active=false, updated_at=now() "
                        "WHERE external_id = ANY(%s);", (list(missing),))
    conn.commit()
    return len(missing)
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_incremental.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/update/ tests/test_incremental.py
git commit -m "feat: инкрементал — upsert POI + каскадный пересчёт, деактивация пропавших"
```

---

### Task 11: Скрапер Циана/Домклика → raw_listings

**Files:**
- Create: `habitus/ingest/cian_scraper.py`
- Test: `tests/test_cian_scraper.py`
- Test: `tests/fixtures/cian_page.html`

**Interfaces:**
- Consumes: `settings`, `load_to_raw` (переиспользуем из Task 3).
- Produces:
  - `habitus.ingest.cian_scraper.parse_listing_html(html: str) -> list[dict]` — HTML страницы выдачи → строки в формате `load_to_raw` (с `description`, source='cian').
  - `habitus.ingest.cian_scraper.scrape(pages: int, http_get=requests.get) -> list[dict]` — обходит N страниц с задержками; `http_get` инъектируется для тестов.

Примечание: парсинг сверяется на сохранённой HTML-фикстуре, не на живом сайте (в CI сети нет и анти-бот). Живой прогон — вручную по запросу.

- [ ] **Step 1: Создать HTML-фикстуру**

Создать `tests/fixtures/cian_page.html` — минимальный HTML с 2 карточками объявлений в структуре, которую парсит `parse_listing_html` (data-атрибуты цены/площади/адреса/описания). Пример каркаса:
```html
<div data-testid="offer-card" data-price="12000000" data-area="54"
     data-rooms="2" data-lat="55.7558" data-lon="37.6173"
     data-id="cian-1001">
  <div class="descr">Светлая квартира, тихий двор, школа рядом</div>
</div>
<div data-testid="offer-card" data-price="9000000" data-area="40"
     data-rooms="1" data-lat="55.7600" data-lon="37.6200"
     data-id="cian-1002">
  <div class="descr">Двор-колодец, окна во двор</div>
</div>
```

- [ ] **Step 2: Написать failing-тест**

```python
# tests/test_cian_scraper.py
from pathlib import Path
from habitus.ingest.cian_scraper import parse_listing_html

FIX = (Path(__file__).parent / "fixtures" / "cian_page.html").read_text(encoding="utf-8")

def test_parse_listing_html():
    rows = parse_listing_html(FIX)
    assert len(rows) == 2
    r = rows[0]
    assert r["external_id"] == "cian_cian-1001"
    assert r["source"] == "cian"
    assert r["price"] == 12000000
    assert r["rooms"] == 2
    assert "школа рядом" in r["description"]
    assert abs(r["lat"] - 55.7558) < 1e-4
```

- [ ] **Step 3: Запустить — FAIL**

Run: `uv run pytest tests/test_cian_scraper.py -v`
Expected: FAIL

- [ ] **Step 4: Реализовать cian_scraper.py**

```python
# habitus/ingest/cian_scraper.py
import time
import requests
from bs4 import BeautifulSoup
from habitus.config import settings

BASE = "https://www.cian.ru/cat.php"

def _int(v):
    try: return int(float(v))
    except (ValueError, TypeError): return None

def parse_listing_html(html: str) -> list[dict]:
    soup = BeautifulSoup(html, "html.parser")
    rows = []
    for card in soup.select('[data-testid="offer-card"]'):
        descr_el = card.select_one(".descr")
        rows.append({
            "external_id": "cian_" + card.get("data-id", ""),
            "source": "cian",
            "price": _int(card.get("data-price")),
            "area": float(card["data-area"]) if card.get("data-area") else None,
            "kitchen_area": None,
            "rooms": _int(card.get("data-rooms")),
            "level": None, "levels": None,
            "building_type": None, "object_type": None,
            "lat": float(card["data-lat"]) if card.get("data-lat") else None,
            "lon": float(card["data-lon"]) if card.get("data-lon") else None,
            "description": descr_el.get_text(strip=True) if descr_el else None,
        })
    return rows

def scrape(pages: int, http_get=requests.get) -> list[dict]:
    all_rows = []
    for page in range(1, pages + 1):
        r = http_get(BASE, params={"deal_type": "sale", "region": 1, "p": page},
                     headers={"User-Agent": "Mozilla/5.0"}, timeout=30)
        r.raise_for_status()
        all_rows.extend(parse_listing_html(r.text))
        time.sleep(3.0)  # вежливая задержка
    return all_rows
```
Добавить `beautifulsoup4>=4.12` в `pyproject.toml` dependencies.
Примечание: реальная разметка Циана отличается и меняется — селекторы `parse_listing_html` подгоняются при первом живом прогоне на сохранённой странице. Тест защищает контракт формата строки, не конкретные селекторы живого сайта.

- [ ] **Step 5: Запустить — PASS**

Run: `uv run pytest tests/test_cian_scraper.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/ingest/cian_scraper.py tests/test_cian_scraper.py tests/fixtures/cian_page.html pyproject.toml
git commit -m "feat: скрапер Циана → raw_listings (парсинг карточек, вежливые задержки)"
```

---

### Task 12: CLI-оркестрация и smoke-прогон

**Files:**
- Create: `habitus/cli.py`
- Modify: `pyproject.toml` (project.scripts)
- Test: `tests/test_cli_smoke.py`

**Interfaces:**
- Consumes: все модули выше.
- Produces:
  - `habitus offline` — полный прогон: init_db → (kaggle CSV из data_dir) load_to_raw → promote_to_listings → backfill_missing_coords → osm fetch/upsert → enrich_all → build doc_text → embed_pending.
  - `habitus update` — инкрементал.
  - Функция `habitus.cli.run_offline(csv_path, conn, model=None, fetch_osm=True) -> dict` — возвращает счётчики шагов (для smoke-теста с моками).

- [ ] **Step 1: Написать smoke-тест (моки модели и OSM, реальная БД)**

```python
# tests/test_cli_smoke.py
import psycopg
from pathlib import Path
from habitus.config import settings
from habitus.cli import run_offline

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"

class FakeModel:
    def encode(self, texts, **kw):
        return {"dense_vecs": [[0.1]*settings.embed_dim for _ in texts],
                "lexical_weights": [{"5":0.5} for _ in texts]}

def test_run_offline_end_to_end():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        stats = run_offline(FIX, conn, model=FakeModel(), fetch_osm=False)
        assert stats["raw"] == 2
        assert stats["listings"] == 2
        assert stats["embedded"] == 2
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings "
                        "WHERE embedding IS NOT NULL AND doc_text IS NOT NULL;")
            assert cur.fetchone()[0] == 2
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_cli_smoke.py -v`
Expected: FAIL

- [ ] **Step 3: Реализовать cli.py**

```python
# habitus/cli.py
import argparse
from pathlib import Path
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.db.connection import get_conn
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
from habitus.clean.normalize import promote_to_listings
from habitus.clean.geocode import backfill_missing_coords
from habitus.geo.osm_extract import fetch_kind, upsert_poi, OVERPASS_QUERIES
from habitus.geo.enrich import enrich_all
from habitus.embed.document import build_doc_text
from habitus.embed.encode import embed_pending

def _refresh_doc_text(conn):
    with conn.cursor(row_factory=psycopg.rows.dict_row) as cur:
        cur.execute("SELECT * FROM listings;")
        rows = cur.fetchall()
    with conn.cursor() as cur:
        for r in rows:
            cur.execute("UPDATE listings SET doc_text=%s WHERE external_id=%s;",
                        (build_doc_text(r), r["external_id"]))
    conn.commit()
    return len(rows)

def run_offline(csv_path: Path, conn, model=None, fetch_osm=True) -> dict:
    init_db(conn)
    stats = {}
    stats["raw"] = load_to_raw(parse_csv(csv_path), conn)
    stats["listings"] = promote_to_listings(conn)
    stats["geocoded"] = backfill_missing_coords(conn)
    if fetch_osm:
        for kind in OVERPASS_QUERIES:
            upsert_poi(fetch_kind(kind), conn)
    stats["enriched"] = enrich_all(conn)
    stats["doc_text"] = _refresh_doc_text(conn)
    stats["embedded"] = embed_pending(conn, model=model)
    return stats

def main():
    ap = argparse.ArgumentParser(prog="habitus")
    sub = ap.add_subparsers(dest="cmd", required=True)
    off = sub.add_parser("offline")
    off.add_argument("--csv", type=Path, required=True)
    off.add_argument("--no-osm", action="store_true")
    sub.add_parser("update")
    args = ap.parse_args()
    with get_conn() as conn:
        if args.cmd == "offline":
            print(run_offline(args.csv, conn, fetch_osm=not args.no_osm))
        elif args.cmd == "update":
            print("update: запускать по cron (инкрементал)")

if __name__ == "__main__":
    main()
```
Добавить в `pyproject.toml`:
```toml
[project.scripts]
habitus = "habitus.cli:main"
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_cli_smoke.py -v`
Expected: PASS

- [ ] **Step 5: Прогнать весь набор тестов**

Run: `uv run pytest -v`
Expected: все тесты PASS.

- [ ] **Step 6: Commit**

```bash
git add habitus/cli.py pyproject.toml tests/test_cli_smoke.py
git commit -m "feat: CLI-оркестрация offline-пайплайна + end-to-end smoke-тест"
```

---

## Итог по покрытию спеки

- Р.2 источники: Task 3 (Kaggle), Task 11 (скрапер), text-ification — Task 8. ✅
- Р.3 архитектура/модули: Tasks 1–12 повторяют структуру. ✅
- Р.4 схема БД: Task 2 (+ raw_listings в Task 3). ✅
- Р.5 гео-обогащение: Task 6 (POI), Task 7 (enrich). Окна/инсоляция — отложены явным примечанием (не в MVP-критпути). ✅
- Р.6 инкрементал: Task 10. ✅
- Р.7 ошибки/надёжность: ретраи геокодинга (Task 5), реиндекс по хэшу (Task 9), идемпотентность (все upsert). ✅
- Р.8 тестирование: pytest на каждый модуль + smoke (Task 12). ✅
- Границы (беки): нигде не пишем API/деплой/ORS. ✅
```
```

## Замечание по окружению
Тесты Tasks 2–12 требуют запущенный `docker compose up -d` (реальный PostGIS). Модель BGE-M3 в тестах всегда мокается — реальная загрузка весов только в живом прогоне `habitus offline`.
