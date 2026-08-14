# Обогащение выдачи и city-скоуп — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Довести до пользователя данные, которые уже собраны (адрес, маршрутное время до метро, слои `urban_evidence`), ввести измерение `city` и подписать каждое показанное число его источником.

**Architecture:** Изменение идёт вертикально через три слоя, но нигде не меняет форму взаимодействия. Новые поля разложены по гибридной схеме: явные колонки для того, что участвует в фильтрации/ранжировании/общем UI, `source_extra jsonb` — для специфики источника. `city` течёт параметром запроса тем же путём, что уже течёт `point`. Шаги 1–6 меняют данные и строго последовательны; 7–13 независимы между собой.

**Tech Stack:** Python 3.12 / psycopg3 / FastAPI / pydantic v2 · Go 1.25 / Fiber / pgx · Next.js 15 / TypeScript / vitest · PostgreSQL 16 + PostGIS + pgvector

**Спека:** `docs/superpowers/specs/2026-08-14-data-enrichment-and-city-scope-design.md`

## Global Constraints

- Коммиты — Conventional Commits на русском (`feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `eval:`, `docs:`). Подписи и трейлеры НЕ используются, никаких `Co-Authored-By`. Работа идёт напрямую в `main`.
- Координаты везде `[lng, lat]`, WGS84 (EPSG:4326). Без трансформаций на фронте.
- Enum'ы зафиксированы на трёх сторонах и меняются вместе: `habitus/online/schema.py` ↔ `backend/internal/service/` ↔ `frontend/lib/agent/types.ts`.
- Не выдумывать факты о городе. Отсутствующее значение деградирует до `NULL`/отсутствия блока; синтетический ноль вместо отсутствующего замера запрещён.
- Python-тесты идут в отдельную БД (`<рабочая>_test`), `tests/conftest.py` подменяет DSN сам. Рабочую БД тесты не трогают.
- Команды: `uv run pytest` · `cd backend && go test ./...` · `cd frontend && npm test`.
- Для шагов с БД нужен поднятый Postgres: `docker compose up -d db`.

## File Structure

**Создаются:**
- `backend/internal/repository/evidence_repo.go` — read-only доступ к `urban_evidence` с bbox-фильтром и упрощением геометрии.

**Меняются (по одной зоне ответственности):**
- `habitus/db/schema.sql` — колонки `city`/`address`/`source_url`/`metro_station`/`walk_min_metro_src`/`source_extra`, индексы.
- `habitus/ingest/cian_loader.py` — перестаёт выбрасывать поля; нормализует массив станций.
- `habitus/ingest/kaggle_loader.py` — только список колонок в `load_to_raw`.
- `habitus/clean/normalize.py` — вывод `metro_station`/`walk_min_metro_src` из `source_extra`.
- `habitus/geo/osm_extract.py` — школы как node+way+relation; `city` при вставке POI.
- `habitus/geo/enrich.py` — KNN вместо декартова произведения, city-скоуп, приоритет источника, три градации шума.
- `habitus/embed/document.py` — адрес и станция в `doc_text`.
- `habitus/online/retrieval.py` — `city` в `build_where`, адрес в `FACT_COLUMNS`.
- `habitus/online/schema.py`, `pipeline.py`, `orchestrator.py`, `geo.py`, `service.py` — проброс `city`.
- `habitus/online/explain.py` — снятие запрета на адрес и станцию.
- `backend/internal/domain/domain.go`, `repository/listing_repo.go`, `service/display_fields.go`, `client/ml_client.go`, `service/search_stream_service.go`, `service/object_service.go` — адрес и город.
- `backend/internal/service/geo_layers_service.go`, `http/handlers/geo_handler.go` — evidence-слои, bbox, лимит.
- `frontend/lib/agent/types.ts`, `lib/format.ts`, `lib/store/session.ts`, `lib/map/useMaplibre.ts`, `components/result/PropertyCard.tsx` — слои, адрес, nullability, переключатель города.
- `backend/migrations/0008_match_score.up.sql` / `.down.sql` — единый `match_score`.

---

### Task 1: Миграция схемы

**Files:**
- Modify: `habitus/db/schema.sql:139-142` (в конец, рядом с существующими `ALTER ... ADD COLUMN IF NOT EXISTS okrug/raion`)
- Test: `tests/test_schema.py`

**Interfaces:**
- Consumes: ничего.
- Produces: колонки `listings.city/address/source_url/metro_station/walk_min_metro_src/source_extra`, `poi.city`, `raw_listings.city/address/source_url/source_extra`. Все последующие задачи опираются на их наличие.

- [ ] **Step 1: Написать падающий тест**

В `tests/test_schema.py` добавить:

```python
def test_enrichment_columns_exist():
    expected = {
        ("listings", "city"), ("listings", "address"),
        ("listings", "source_url"), ("listings", "metro_station"),
        ("listings", "walk_min_metro_src"), ("listings", "source_extra"),
        ("poi", "city"),
        ("raw_listings", "city"), ("raw_listings", "address"),
        ("raw_listings", "source_url"), ("raw_listings", "source_extra"),
    }
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT table_name, column_name FROM information_schema.columns
                           WHERE table_schema='public'
                             AND table_name IN ('listings','poi','raw_listings');""")
            got = {(t, c) for t, c in cur.fetchall()}
    assert expected <= got, f"нет колонок: {expected - got}"


def test_existing_rows_default_to_moscow():
    # DEFAULT 'msk' делает backfill бесплатным: всё, что уже в базе, — московское
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("INSERT INTO listings (external_id, source) VALUES ('C1','test');")
            cur.execute("SELECT city, source_extra FROM listings WHERE external_id='C1';")
            city, extra = cur.fetchone()
    assert city == "msk"
    assert extra == {}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `docker compose up -d db && uv run pytest tests/test_schema.py::test_enrichment_columns_exist -v`
Expected: FAIL — `нет колонок: {('listings', 'city'), ...}`

- [ ] **Step 3: Добавить колонки**

В конец `habitus/db/schema.sql`:

```sql
-- Обогащение из полей источника, которые загрузчик раньше выбрасывал.
-- Гибридная схема: явные колонки — для того, что участвует в фильтрации,
-- ранжировании или общем для всех городов UI; source_extra — для специфики
-- источника (у Циана zhk/building_material, у Дубая будут community/developer).
ALTER TABLE listings ADD COLUMN IF NOT EXISTS city               text NOT NULL DEFAULT 'msk';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS address            text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS source_url         text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS metro_station      text;
-- Время до метро ОТ ИСТОЧНИКА. Отдельно от walk_min_metro, чтобы не потерять
-- провенанс: итог = COALESCE(walk_min_metro_src, вычисленное по OSM).
ALTER TABLE listings ADD COLUMN IF NOT EXISTS walk_min_metro_src real;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS source_extra       jsonb NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS listings_city_ix ON listings (city);

ALTER TABLE poi ADD COLUMN IF NOT EXISTS city text NOT NULL DEFAULT 'msk';
CREATE INDEX IF NOT EXISTS poi_city_kind_ix ON poi (city, kind);

-- raw_listings — зеркало источника: производные поля выводит promote_to_listings.
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS city         text NOT NULL DEFAULT 'msk';
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS address      text;
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS source_url   text;
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS source_extra jsonb NOT NULL DEFAULT '{}';
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_schema.py -v`
Expected: PASS, все тесты файла

- [ ] **Step 5: Коммит**

```bash
git add habitus/db/schema.sql tests/test_schema.py
git commit -m "feat: колонки обогащения и city в схеме listings/poi/raw_listings"
```

---

### Task 2: Разбор полей Циана

**Files:**
- Modify: `habitus/ingest/cian_loader.py:26-47`
- Test: `tests/test_cian_loader.py`

**Interfaces:**
- Consumes: ничего (чистый разбор, без БД).
- Produces:
  - `parse_metro(raw: str) -> list[dict]` — Циановский JSON → нормализованные записи `{"name": str, "minutes": int, "mode": "walk" | "transport"}`. Битый/пустой вход → `[]`.
  - `parse_csv(path) -> list[dict]` — каждый dict дополнительно содержит `city: "msk"`, `address: str | None`, `source_url: str | None`, `source_extra: dict` с ключами `metro` (нормализованный список), `zhk`, `building_material`, `deadline`.

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_cian_loader.py` добавить:

```python
from habitus.ingest.cian_loader import parse_metro


def test_parse_metro_normalizes_entries():
    raw = ('[{"name":"Ленинский проспект","time":7,"transport_type":"walk"},'
           '{"name":"Шаболовская","time":3,"transport_type":"transport"}]')
    assert parse_metro(raw) == [
        {"name": "Ленинский проспект", "minutes": 7, "mode": "walk"},
        {"name": "Шаболовская", "minutes": 3, "mode": "transport"},
    ]


def test_parse_metro_survives_broken_input():
    # Битый JSON не должен ронять строку: объявление грузится, работает OSM-фолбэк
    for raw in ("", "   ", "не json", "{}", "null", None):
        assert parse_metro(raw) == []


def test_parse_csv_keeps_address_url_and_extra():
    rows = parse_csv(FIX)
    row = rows[0]
    assert row["city"] == "msk"
    assert row["address"]
    assert row["source_url"].startswith("http")
    assert isinstance(row["source_extra"]["metro"], list)
    assert "zhk" in row["source_extra"]
```

Если в фикстуре `tests/fixtures/sample_cian.csv` нет колонок `address`/`url`/`metro`/`zhk`/`building_material`/`deadline` — дописать их, скопировав заголовок и одну строку из рабочего `listings.csv`.

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_cian_loader.py -v`
Expected: FAIL — `ImportError: cannot import name 'parse_metro'`

- [ ] **Step 3: Реализовать**

В `habitus/ingest/cian_loader.py` добавить импорт `json` и функцию, затем расширить `parse_csv`:

```python
import json

# Циан отдаёт станции как JSON-массив {name, time, transport_type}. Приводим к
# общей форме {name, minutes, mode}: у следующего источника (Дубай) поля будут
# называться иначе, а promote_to_listings должен читать одинаково.
def parse_metro(raw: str | None) -> list[dict]:
    if not raw or not str(raw).strip():
        return []
    try:
        items = json.loads(raw)
    except (ValueError, TypeError):
        return []
    if not isinstance(items, list):
        return []
    out = []
    for it in items:
        if not isinstance(it, dict):
            continue
        minutes, name = _to_int(it.get("time")), (it.get("name") or "").strip()
        if minutes is None or not name:
            continue
        mode = "walk" if it.get("transport_type") == "walk" else "transport"
        out.append({"name": name, "minutes": minutes, "mode": mode})
    return out
```

В теле цикла `parse_csv`, внутри `out.append({...})`, добавить к существующим ключам:

```python
                "city": "msk",
                "address": (row.get("address") or "").strip() or None,
                "source_url": (row.get("url") or "").strip() or None,
                "source_extra": {
                    "metro": parse_metro(row.get("metro")),
                    "zhk": (row.get("zhk") or "").strip() or None,
                    "building_material": (row.get("building_material") or "").strip() or None,
                    "deadline": (row.get("deadline") or "").strip() or None,
                },
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_cian_loader.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/ingest/cian_loader.py tests/test_cian_loader.py tests/fixtures/sample_cian.csv
git commit -m "feat: загрузчик Циана перестаёт выбрасывать адрес, url и станции метро"
```

---

### Task 3: Сырой слой и промоушен

**Files:**
- Modify: `habitus/ingest/kaggle_loader.py:44-45`
- Modify: `habitus/clean/normalize.py:23-43`
- Test: `tests/test_normalize.py`

**Interfaces:**
- Consumes: `parse_csv` из Task 2 (ключи `city`, `address`, `source_url`, `source_extra`).
- Produces: `pick_walk_metro(entries: list[dict]) -> tuple[str | None, float | None]` — имя станции и минуты минимальной пешей записи; `(None, None)`, если пеших нет. `promote_to_listings` заполняет `city`, `address`, `source_url`, `source_extra`, `metro_station`, `walk_min_metro_src`.

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_normalize.py` добавить:

```python
import json
import psycopg
from habitus.clean.normalize import pick_walk_metro
from habitus.config import settings
from habitus.db.init_db import init_db


def test_pick_walk_metro_takes_nearest_walk_entry():
    entries = [
        {"name": "Шаболовская", "minutes": 3, "mode": "transport"},
        {"name": "Ленинский проспект", "minutes": 7, "mode": "walk"},
        {"name": "Площадь Гагарина", "minutes": 10, "mode": "walk"},
    ]
    # 3 минуты — это автобусом, колонка называется walk_min: берём 7
    assert pick_walk_metro(entries) == ("Ленинский проспект", 7.0)


def test_pick_walk_metro_without_walk_entries():
    assert pick_walk_metro([{"name": "X", "minutes": 4, "mode": "transport"}]) == (None, None)
    assert pick_walk_metro([]) == (None, None)


def test_promote_carries_source_fields_into_listings():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE raw_listings, listings;")
            cur.execute("""
                INSERT INTO raw_listings (external_id, source, price, area, rooms,
                                          lat, lon, city, address, source_url, source_extra)
                VALUES ('cian_1','cian',20000000,55,2,55.71,37.59,'msk',
                        'Москва, 2-й Донской проезд','https://cian.ru/1',%s);""",
                        (json.dumps({"metro": [
                            {"name": "Ленинский проспект", "minutes": 7, "mode": "walk"}],
                            "zhk": "SHIFT"}),))
        conn.commit()
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT city, address, source_url, metro_station,
                                  walk_min_metro_src, source_extra->>'zhk'
                           FROM listings WHERE external_id='cian_1';""")
            row = cur.fetchone()
    assert row == ("msk", "Москва, 2-й Донской проезд", "https://cian.ru/1",
                   "Ленинский проспект", 7.0, "SHIFT")
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_normalize.py -v`
Expected: FAIL — `ImportError: cannot import name 'pick_walk_metro'`

- [ ] **Step 3: Реализовать**

В `habitus/ingest/kaggle_loader.py` расширить список колонок в `load_to_raw` (значения по умолчанию нужны, потому что kaggle-строки этих ключей не содержат):

```python
    cols = ["external_id","source","price","area","kitchen_area","rooms",
            "level","levels","building_type","object_type","lat","lon","description",
            "city","address","source_url","source_extra"]
    rows = [{**{"city": "msk", "address": None, "source_url": None,
                "source_extra": Json({})}, **r} for r in rows]
```

с `from psycopg.types.json import Json` в импортах; если ключ `source_extra` пришёл словарём — обернуть: `r["source_extra"] = Json(r["source_extra"])`.

В `habitus/clean/normalize.py` добавить:

```python
def pick_walk_metro(entries: list[dict]) -> tuple[str | None, float | None]:
    """Ближайшая ПЕШАЯ станция из нормализованного списка source_extra['metro'].

    Записи с mode='transport' игнорируются: это время на транспорте, а колонка
    называется walk_min_metro. Нет пеших записей → (None, None), и дальше
    сработает OSM-фолбэк в enrich.
    """
    walk = [e for e in entries or []
            if e.get("mode") == "walk" and e.get("minutes") is not None]
    if not walk:
        return None, None
    best = min(walk, key=lambda e: e["minutes"])
    return best.get("name") or None, float(best["minutes"])
```

и расширить `promote_to_listings`: перед формированием `valid` вывести производные поля,

```python
    for r in valid:
        station, minutes = pick_walk_metro((r.get("source_extra") or {}).get("metro"))
        r["metro_station"], r["walk_min_metro_src"] = station, minutes
        r["source_extra"] = Json(r.get("source_extra") or {})
```

а в SQL — добавить колонки в список вставки и в `DO UPDATE SET`:

```sql
        INSERT INTO listings
          (external_id, source, price, area, kitchen_area, rooms, level, levels,
           building_type, object_type, geom, description,
           city, address, source_url, source_extra, metro_station, walk_min_metro_src)
        VALUES
          (%(external_id)s, %(source)s, %(price)s, %(area)s, %(kitchen_area)s,
           %(rooms)s, %(level)s, %(levels)s, %(building_type)s, %(object_type)s,
           ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326), %(description)s,
           %(city)s, %(address)s, %(source_url)s, %(source_extra)s,
           %(metro_station)s, %(walk_min_metro_src)s)
        ON CONFLICT (external_id) DO UPDATE SET
           price=EXCLUDED.price, area=EXCLUDED.area, geom=EXCLUDED.geom,
           description=EXCLUDED.description, city=EXCLUDED.city,
           address=EXCLUDED.address, source_url=EXCLUDED.source_url,
           source_extra=EXCLUDED.source_extra, metro_station=EXCLUDED.metro_station,
           walk_min_metro_src=EXCLUDED.walk_min_metro_src,
           is_active=true, updated_at=now();
```

`promote_to_listings` читает `SELECT * FROM raw_listings`, поэтому новые колонки приезжают в `r` автоматически.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_normalize.py tests/test_kaggle_loader.py tests/test_cli_smoke.py -v`
Expected: PASS — `test_cli_smoke` проверяет, что kaggle-путь не сломался

- [ ] **Step 5: Коммит**

```bash
git add habitus/ingest/kaggle_loader.py habitus/clean/normalize.py tests/test_normalize.py
git commit -m "feat: промоушен адреса, url и станции метро из сырого слоя в listings"
```

---

### Task 4: Школы в Overpass

**Files:**
- Modify: `habitus/geo/osm_extract.py:20`, `:141-152` (`upsert_poi`)
- Test: `tests/test_osm_extract.py`

**Interfaces:**
- Consumes: `poi.city` из Task 1.
- Produces: `upsert_poi(rows, conn, city="msk") -> int`. Запрос школ покрывает node+way+relation.

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_osm_extract.py` добавить:

```python
def test_school_query_covers_polygons():
    # Школьные здания в OSM — это way/relation, а не node. Для парков это уже
    # учтено; из-за node-only запроса в базе оказалось 173 школы вместо ~1500.
    q = OVERPASS_QUERIES["school"]
    assert 'way["amenity"="school"]' in q
    assert 'relation["amenity"="school"]' in q
    assert q.startswith("(") and q.endswith(");")


def test_upsert_poi_sets_city():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE poi;")
        conn.commit()
        upsert_poi([{"osm_id": 1, "kind": "school", "name": "Школа",
                     "lat": 55.75, "lon": 37.61}], conn)
        with conn.cursor() as cur:
            cur.execute("SELECT city FROM poi WHERE osm_id=1;")
            assert cur.fetchone()[0] == "msk"
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_osm_extract.py -v`
Expected: FAIL — `assert 'way["amenity"="school"]' in 'node["amenity"="school"](...)'`

- [ ] **Step 3: Реализовать**

В `OVERPASS_QUERIES` заменить строку школ (форма — та же, что уже работает для парков; `parse_overpass` уже берёт `el["center"]` для way/relation, менять его не нужно):

```python
    # Школьные здания в OSM — way/relation, а не node: node-only запрос давал
    # 173 школы на Москву вместо ~1500, и walk_min_school врал.
    "school":     f'(node["amenity"="school"]{MSK_AREA};'
                  f'way["amenity"="school"]{MSK_AREA};'
                  f'relation["amenity"="school"]{MSK_AREA};);',
```

В `upsert_poi` добавить город:

```python
def upsert_poi(rows: list[dict], conn: psycopg.Connection, city: str = "msk") -> int:
    sql = """
        INSERT INTO poi (osm_id, kind, name, geom, city)
        VALUES (%(osm_id)s, %(kind)s, %(name)s,
                ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326), %(city)s)
        ON CONFLICT (osm_id, kind) DO UPDATE SET
            name=EXCLUDED.name, geom=EXCLUDED.geom, city=EXCLUDED.city,
            updated_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, [{**r, "city": city} for r in rows])
    conn.commit()
    return len(rows)
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_osm_extract.py tests/test_incremental.py -v`
Expected: PASS — `test_incremental` вызывает `upsert_poi` и проверяет, что сигнатура совместима

- [ ] **Step 5: Коммит**

```bash
git add habitus/geo/osm_extract.py tests/test_osm_extract.py
git commit -m "fix: школы из OSM собираются вместе с полигонами way/relation"
```

---

### Task 5: enrich на KNN, city-скоуп и приоритет источника

**Files:**
- Modify: `habitus/geo/enrich.py:10-37`
- Test: `tests/test_enrich.py`

**Interfaces:**
- Consumes: `poi.city`, `listings.city`, `listings.walk_min_metro_src` из Tasks 1/3/4.
- Produces: `enrich_all(conn) -> int`, `enrich_around(conn, wkt) -> int` — сигнатуры не меняются. `walk_min_metro = COALESCE(walk_min_metro_src, вычисленное)`.

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_enrich.py` добавить:

```python
def test_source_metro_time_wins_over_computed():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            # станция в ~1.5 км → вычисленное время ~19 мин
            cur.execute("""INSERT INTO poi (osm_id, kind, name, geom, city) VALUES
                (1,'metro','Дальняя',ST_SetSRID(ST_MakePoint(37.63,55.75),4326),'msk');""")
            cur.execute("""INSERT INTO listings (external_id, source, geom, city,
                                                 walk_min_metro_src) VALUES
                ('WITH_SRC','t',ST_SetSRID(ST_MakePoint(37.61,55.75),4326),'msk',7),
                ('NO_SRC','t',ST_SetSRID(ST_MakePoint(37.61,55.75),4326),'msk',NULL);""")
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT external_id, walk_min_metro FROM listings ORDER BY external_id;")
            got = dict(cur.fetchall())
    assert got["WITH_SRC"] == 7.0            # источник не перезаписан
    assert got["NO_SRC"] > 10                # посчитано по OSM


def test_enrich_does_not_cross_cities():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO poi (osm_id, kind, name, geom, city) VALUES
                (2,'school','Чужая',ST_SetSRID(ST_MakePoint(37.611,55.75),4326),'dxb');""")
            cur.execute("""INSERT INTO listings (external_id, source, geom, city) VALUES
                ('M1','t',ST_SetSRID(ST_MakePoint(37.61,55.75),4326),'msk');""")
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT walk_min_school FROM listings WHERE external_id='M1';")
            assert cur.fetchone()[0] is None    # школа чужого города не считается
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_enrich.py -v`
Expected: FAIL — `WITH_SRC` перезаписан вычисленным значением; чужая школа посчитана

- [ ] **Step 3: Реализовать**

Заменить `_ENRICH_SQL` в `habitus/geo/enrich.py`. Ключевое изменение — подзапросы вида `MIN(ST_Distance(...)) FROM poi WHERE kind=...` (полный перебор, GIST не используется) становятся KNN через `<->`:

```python
# Ближайший POI ищем KNN-оператором <-> (по нему работает GIST), но берём пять
# кандидатов, а не одного: <-> упорядочивает по ПЛАНАРНОМУ расстоянию в градусах,
# а планарно ближайший на широте Москвы не всегда геодезически ближайший.
# Итоговое расстояние по-прежнему считается через geography — значения
# сопоставимы с прежними.
def _nearest_min(kind: str) -> str:
    return f"""(
      SELECT MIN(ST_Distance(l.geom::geography, p.geom::geography)) / {WALK_SPEED_MPS} / 60.0
      FROM (SELECT geom FROM poi
            WHERE kind = '{kind}' AND city = l.city
            ORDER BY geom <-> l.geom LIMIT 5) p
    )"""


_ENRICH_SQL = f"""
UPDATE listings l SET
  bar_density_500m = (
    SELECT count(*) FROM poi p
    WHERE p.kind IN ('bar','alcohol') AND p.city = l.city
      AND ST_DWithin(l.geom::geography, p.geom::geography, %(radius)s)
  ),
  walk_min_school = {_nearest_min('school')},
  walk_min_park   = {_nearest_min('park')},
  -- источник в приоритете, вычисленное — фолбэк (см. спеку: провенанс)
  walk_min_metro  = COALESCE(l.walk_min_metro_src, {_nearest_min('metro')}),
  noise_level = CASE
    WHEN (SELECT count(*) FROM poi p WHERE p.kind='bar' AND p.city = l.city
          AND ST_DWithin(l.geom::geography, p.geom::geography, 200)) > 2 THEN 'high'
    ELSE 'low' END,
  updated_at = now()
WHERE l.geom IS NOT NULL
  AND (%(filter_geog)s::text IS NULL
       OR ST_DWithin(l.geom::geography, ST_GeogFromText(%(filter_geog)s::text), %(radius)s));
"""
```

`kind` подставляется из литерала внутри модуля, не из пользовательского ввода — инъекции нет; `poi_geom_wkt` по-прежнему едет биндом.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_enrich.py tests/test_incremental.py tests/test_proximity.py -v`
Expected: PASS — включая существующий `test_enrich_around_wkt_not_injectable`

- [ ] **Step 5: Коммит**

```bash
git add habitus/geo/enrich.py tests/test_enrich.py
git commit -m "perf: гео-обогащение через KNN вместо полного перебора POI, скоуп по городу"
```

---

### Task 6: Адрес в doc_text и переэмбеддинг

**Files:**
- Modify: `habitus/embed/document.py:8-34`
- Test: `tests/test_document.py`

**Interfaces:**
- Consumes: `listings.address`, `listings.metro_station` из Task 3.
- Produces: `build_doc_text(row)` включает адрес и станцию. `content_hash` у всех строк меняется → `embed_pending` пересчитает эмбеддинги целиком.

- [ ] **Step 1: Написать падающий тест**

В `tests/test_document.py` добавить:

```python
def test_doc_text_includes_address_and_station():
    text = build_doc_text({
        "description": "Светлая квартира", "rooms": 2, "area": 54.0,
        "address": "Москва, Хамовники, Комсомольский проспект",
        "metro_station": "Парк культуры", "walk_min_metro": 7.0,
    })
    assert "Хамовники" in text
    assert "Парк культуры" in text


def test_doc_text_without_address_is_unchanged():
    # Отсутствие адреса не должно давать пустых фрагментов вида ", , "
    text = build_doc_text({"rooms": 1, "area": 30.0})
    assert ", ," not in text
    assert text.startswith("1-комн")
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `uv run pytest tests/test_document.py -v`
Expected: FAIL — `assert 'Хамовники' in '...'`

- [ ] **Step 3: Реализовать**

В `build_doc_text`, сразу после блока с `description` (адрес — сильный семантический сигнал для запросов вида «сталинка в Хамовниках», поэтому идёт в начало, где реранкер его точно увидит):

```python
    if row.get("address"):
        parts.append(row["address"].strip())
```

и перед блоком `wm = row.get("walk_min_metro")`:

```python
    if row.get("metro_station"):
        parts.append(f"метро {row['metro_station']}")
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_document.py tests/test_encode.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/embed/document.py tests/test_document.py
git commit -m "feat: адрес и станция метро в тексте документа для эмбеддинга"
```

- [ ] **Step 6: Снять базовую линию eval ДО перезаливки**

Run: `uv run habitus eval > /tmp/eval-before.md && cat /tmp/eval-before.md`
Сохранить вывод — после перезаливки метрики сдвинутся, и без этой точки отсчёта сдвиг будет выглядеть регрессией.

- [ ] **Step 7: Перезалить данные и пересчитать**

```bash
uv run habitus offline --csv listings.csv --source cian
uv run habitus eval > /tmp/eval-after.md
diff /tmp/eval-before.md /tmp/eval-after.md
```

Ожидается: recall/NDCG/MRR сдвигаются (новые `walk_min_school` после досбора школ + адрес в тексте).

- [ ] **Step 8: Зафиксировать новую базовую линию в репозитории**

```bash
mkdir -p docs/notes
cp /tmp/eval-after.md docs/notes/eval-baseline-2026-08-14.md
git add docs/notes/eval-baseline-2026-08-14.md
git commit -m "eval: базовая линия после досбора школ и адреса в doc_text"
```

Файлы `habitus/eval/*.py` этой задачей НЕ трогаются: в рабочем дереве уже лежит
незакоммиченная правка с метрикой MRR, не относящаяся к этому плану. Не смешивать.

---

### Task 7: Проброс city через ML-контракт

**Files:**
- Modify: `habitus/online/schema.py:105-108`, `habitus/online/retrieval.py:41-67,116-164`, `habitus/online/orchestrator.py:35-90`, `habitus/online/pipeline.py:25-123`, `habitus/online/geo.py:162-243`, `habitus/online/service.py:38-50`
- Test: `tests/test_retrieval.py`, `tests/test_pipeline.py`

**Interfaces:**
- Consumes: `listings.city` из Task 1.
- Produces:
  - `SearchRequest.city: Literal["msk","spb"] = "msk"`
  - `build_where(pq, extra_sql=None, extra_params=(), city=None) -> tuple[str, list]`
  - `hybrid_search(conn, pq, *, ..., city=None)`, `filter_only_search(conn, pq, top_k=None, geo_sql=None, geo_params=(), city=None)`
  - `retrieve_with_relaxation(conn, pq, *, ..., city=None)`
  - `run_search(query, conn, *, ..., city="msk")`
  - `resolve_area(area, conn=None, *, geocoder=geocode_address, city="msk")`

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_retrieval.py` добавить:

```python
def test_build_where_scopes_by_city():
    where, params = build_where(ParsedQuery(), city="msk")
    assert "city = %s" in where
    assert "msk" in params


def test_build_where_without_city_is_unscoped():
    where, params = build_where(ParsedQuery())
    assert "city" not in where
```

В `tests/test_pipeline.py` добавить (фикстура `conn` там уже есть):

```python
def test_search_does_not_return_other_cities(conn):
    with conn.cursor() as cur:
        cur.execute("""INSERT INTO listings (external_id, source, is_active, city,
                                             doc_text, geom)
            VALUES ('DXB1','t',TRUE,'dxb','квартира',
                    ST_SetSRID(ST_MakePoint(55.27,25.20),4326));""")
    conn.commit()
    resp = run_search("квартира", conn, city="msk")
    assert all(r.external_id != "DXB1" for r in resp.results)
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_retrieval.py tests/test_pipeline.py -v`
Expected: FAIL — `TypeError: build_where() got an unexpected keyword argument 'city'`

- [ ] **Step 3: Реализовать проброс**

`habitus/online/retrieval.py` — в `build_where` добавить параметр и клаузу первой (город — самый селективный фильтр):

```python
def build_where(pq: ParsedQuery, extra_sql: str | None = None,
                extra_params: Sequence = (), city: str | None = None) -> tuple[str, list]:
    clauses: list[str] = ["is_active = TRUE"]
    params: list = []
    if city:
        clauses.append("city = %s"); params.append(city)
```

В `filter_only_search` и `hybrid_search` добавить параметр `city: str | None = None` и передать его в оба вызова `build_where(pq, geo_sql, geo_params, city)`. В `hybrid_search` пробросить `city` и в `filter_only_search`.

`habitus/online/orchestrator.py` — в `retrieve_with_relaxation` добавить `city: str | None = None` и передавать в оба вызова `search_fn(...)` как `city=city`.

`habitus/online/pipeline.py` — в `run_search` добавить `city: str = "msk"`, передать в `retrieve_with_relaxation(..., city=city)` и в `resolve_area(pq.area, conn, city=city)`.

`habitus/online/geo.py` — в `resolve_area` добавить `city: str = "msk"` и заменить два хардкода Nominatim:

```python
CITY_GEOCODE_SUFFIX = {"msk": "Москва", "spb": "Санкт-Петербург"}
```

```python
    suffix = CITY_GEOCODE_SUFFIX.get(city, "")
    query = f"{area}, {suffix}" if suffix else area
```

и использовать `query` в обоих вызовах геокодера. Таблица `CARDINAL` остаётся московской: для другого города округа просто не совпадут и резолв уйдёт в геокодер — это деградация, а не ошибка.

`habitus/online/schema.py` — в `SearchRequest`:

```python
    city: Literal["msk", "spb"] = "msk"
```

`habitus/online/service.py` — в эндпоинте `/search`: `return run_search(req.query, conn, llm=llm, point=req.point, provider=provider, city=req.city)`.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_retrieval.py tests/test_retrieval_db.py tests/test_pipeline.py tests/test_orchestrator.py tests/test_area.py tests/test_service.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/online/ tests/test_retrieval.py tests/test_pipeline.py
git commit -m "feat: город как параметр запроса от API до SQL-фильтра"
```

---

### Task 8: Адрес в фактах и в промпте объяснения

**Files:**
- Modify: `habitus/online/retrieval.py:16-17` (`FACT_COLUMNS`)
- Modify: `habitus/online/explain.py:7-13`
- Test: `tests/test_explain.py`

**Interfaces:**
- Consumes: `listings.address`, `listings.metro_station` из Task 3.
- Produces: `Candidate.facts` и `ResultItem.address_facts` содержат `address` и `metro_station`.

- [ ] **Step 1: Написать падающий тест**

В `tests/test_explain.py` добавить:

```python
def test_facts_block_carries_address_and_station():
    item = ResultItem(external_id="A", price=20000000, area=54.0, rooms=2,
                      address_facts={"address": "Москва, Хамовники",
                                     "metro_station": "Парк культуры",
                                     "walk_min_metro": 7.0}, score=0.9)
    block = facts_block([item], [])
    assert "Хамовники" in block
    assert "Парк культуры" in block


def test_prompt_allows_address_but_still_forbids_invented_geography():
    assert "адрес" in GROUNDED_SYSTEM.lower()
    assert "школ" in GROUNDED_SYSTEM.lower()   # запрет на названия школ остаётся
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `uv run pytest tests/test_explain.py -v`
Expected: FAIL на втором тесте — текущий промпт запрещает адреса целиком

- [ ] **Step 3: Реализовать**

`habitus/online/retrieval.py` — расширить `FACT_COLUMNS`:

```python
FACT_COLUMNS = ("walk_min_school", "walk_min_metro", "walk_min_park",
                "bar_density_500m", "noise_level", "window_orientation",
                "address", "metro_station")
```

`habitus/online/explain.py` — переписать запрет так, чтобы он снимался ровно с двух полей, которые теперь grounded:

```python
GROUNDED_SYSTEM = """Ты — ассистент по недвижимости. Объясни пользователю подбор \
квартир по его запросу.
ЖЁСТКОЕ ПРАВИЛО: используй ТОЛЬКО данные из блока ФАКТЫ. Адрес и станцию метро \
называть можно — но строго теми значениями, что стоят в полях address и \
metro_station. Запрещено называть названия школ, ЖК, застройщиков и любую \
географию, которой нет в ФАКТАХ. Если каких-то данных нет — просто не упоминай их.
Если в ФАКТАХ есть строка «ОСЛАБЛЕНО», честно скажи, какие условия пришлось ослабить.
Отвечай на языке запроса пользователя, кратко: 3-6 предложений."""
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_explain.py tests/test_retrieval_db.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/online/retrieval.py habitus/online/explain.py tests/test_explain.py
git commit -m "feat: адрес и станция метро в фактах объяснения"
```

---

### Task 9: Go — адрес и город

**Files:**
- Modify: `backend/internal/domain/domain.go:74-83`, `repository/listing_repo.go:24-54`, `service/display_fields.go:19-30,99-133`, `client/ml_client.go:85-88`, `service/search_stream_service.go:139`, `service/object_service.go:302-325,383`
- Test: `backend/internal/service/display_fields_test.go` (создать), `backend/internal/client/ml_client_test.go`

**Interfaces:**
- Consumes: `SearchRequest.city` из Task 7; колонки `address`/`metro_station` из Task 3.
- Produces: `domain.Listing` с полями `Address *string`, `MetroStation *string`; `FinalResultObject.Address string`; `client.SearchRequest.City string`.

- [ ] **Step 1: Написать падающие тесты**

Создать `backend/internal/service/display_fields_test.go`:

```go
package service

import (
	"testing"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
)

func strp(s string) *string { return &s }

func TestBuildFinalResultObjectPrefersRealAddress(t *testing.T) {
	lon, lat := 37.61, 55.75
	listings := map[string]domain.Listing{"A": {
		ExternalID: "A", Lon: &lon, Lat: &lat,
		Address: strp("Москва, 2-й Донской проезд"),
	}}
	obj, ok := BuildFinalResultObject(client.ResultItem{ExternalID: "A"}, 0, nil, listings)
	if !ok {
		t.Fatal("объект должен собраться")
	}
	if obj.Address != "Москва, 2-й Донской проезд" {
		t.Fatalf("адрес не доехал: %q", obj.Address)
	}
}

func TestBuildFinalResultObjectFallsBackToSynthName(t *testing.T) {
	lon, lat := 37.61, 55.75
	rooms, area := 2, 54.0
	listings := map[string]domain.Listing{"A": {
		ExternalID: "A", Lon: &lon, Lat: &lat, Rooms: &rooms, Area: &area,
	}}
	obj, _ := BuildFinalResultObject(client.ResultItem{ExternalID: "A"}, 0, nil, listings)
	if obj.Address != "" {
		t.Fatalf("адреса нет — поле должно быть пустым, получено %q", obj.Address)
	}
	if obj.Name != "2-комн, 54 м²" {
		t.Fatalf("должен сработать SynthName, получено %q", obj.Name)
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `cd backend && go test ./internal/service/ -run TestBuildFinalResultObject -v`
Expected: FAIL — `unknown field Address in struct literal`

- [ ] **Step 3: Реализовать**

`domain.go` — в `Listing` добавить:

```go
	Address      *string
	MetroStation *string
```

`listing_repo.go` — расширить `scanListing` и SELECT:

```go
func scanListing(rows pgx.Rows) (domain.Listing, error) {
	var l domain.Listing
	err := rows.Scan(&l.ExternalID, &l.Price, &l.Area, &l.Rooms, &l.Level,
		&l.Levels, &l.Lon, &l.Lat, &l.Address, &l.MetroStation)
	return l, err
}
```

```go
		SELECT external_id, price, area, rooms, level, levels,
		       ST_X(geom), ST_Y(geom), address, metro_station
		FROM listings WHERE external_id = ANY($1)`, ids)
```

`display_fields.go` — добавить поле в `FinalResultObject` (после `Name`):

```go
	Address     string    `json:"address"`
```

и заполнить его в `BuildFinalResultObject`:

```go
	address := ""
	if l.Address != nil {
		address = *l.Address
	}
```
```go
		Address:     address,
```

`ml_client.go` — в `SearchRequest`:

```go
type SearchRequest struct {
	Query string           `json:"query"`
	City  string           `json:"city,omitempty"`
	Point *PointConstraint `json:"point,omitempty"`
}
```

`search_stream_service.go` — в вызове `s.ml.Search` передать город чата. Сигнатура `Run` уже принимает `chat domain.Chat`:

```go
		resp, err := s.ml.Search(mlCtx, client.SearchRequest{
			Query: text, City: chat.City, Point: point})
```

`object_service.go` — заменить захардкоженный город на город чата. Сигнатура `dossier` получает `city`:

```go
func (s *ObjectService) dossier(ctx context.Context, chatID uuid.UUID, objectID, city string,
	res domain.ChatSearchResult) (DossierPayload, bool) {
```

в теле — `City: city` вместо литерала `"msk"`:

```go
	response, err := s.ml.Dossier(mlCtx, client.DossierRequest{
		ObjectID: objectID, City: city, RawQuery: search.RawQuery,
		ParsedQuery: search.ParsedQuery, Relaxed: nonNilStrings(search.Relaxed),
		Degraded: nonNilStrings(search.Degraded),
	})
```

и в `GetPassport` — вызов с городом чата плюс реальный адрес вместо пустой строки:

```go
	if chat.City == "msk" && s.ml != nil {
		if dossier, ok := s.dossier(ctx, chatID, objectID, chat.City, res); ok {
```

```go
	address := ""
	if listing.Address != nil {
		address = *listing.Address
	}
```
```go
		Address:           address,
```

Условие `if chat.City == "msk"` оставить: досье пока считается только по московским слоям.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `cd backend && go vet ./... && go test ./... -v 2>&1 | tail -30`
Expected: PASS, все пакеты

- [ ] **Step 5: Коммит**

```bash
git add backend/internal/
git commit -m "feat: адрес объекта и город чата в контракте шлюза"
```

---

### Task 10: Evidence-слои с bbox и лимитом

**Files:**
- Create: `backend/internal/repository/evidence_repo.go`
- Modify: `backend/internal/service/geo_layers_service.go:14-93`, `http/handlers/geo_handler.go:21-33`, `backend/internal/domain/domain.go`, `cmd/api/main.go:64`
- Test: `backend/internal/service/geo_layers_service_test.go`

**Interfaces:**
- Consumes: таблицу `urban_evidence` (существует).
- Produces: `EvidenceRepo.ListByLayers(ctx, city string, layers []string, bbox [4]float64, limit int) ([]domain.EvidenceFeature, error)`; `GeoLayersService.Layers(ctx, city string, requested []string, bbox *[4]float64) (map[string]geojson.FeatureCollection, map[string]bool, error)` — второй возврат это `truncated` по слоям.

**Константы (из спеки):** допуск упрощения `0.0001`, предел `5000` фич на слой, порядок bbox `minLon,minLat,maxLon,maxLat`.

- [ ] **Step 1: Написать падающие тесты**

В `backend/internal/service/geo_layers_service_test.go` уже есть `fakePOILister`; рядом с ним добавить фейк для evidence и два теста. Существующие вызовы `NewGeoLayersService(repo)` в этом файле надо обновить до `NewGeoLayersService(repo, &fakeEvidenceLister{})`, иначе пакет не соберётся.

```go
type fakeEvidenceLister struct{ rows []domain.EvidenceFeature }

func (f *fakeEvidenceLister) ListByLayers(_ context.Context, _ string, _ []string,
	_ [4]float64, _ int) ([]domain.EvidenceFeature, error) {
	return f.rows, nil
}

func TestEvidenceLayerRequiresBbox(t *testing.T) {
	svc := NewGeoLayersService(&fakePOILister{}, &fakeEvidenceLister{})
	out, truncated, err := svc.Layers(context.Background(), "msk", []string{"communal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["communal"].Features) != 0 {
		t.Fatal("без bbox evidence-слой должен быть пустым, а не 500")
	}
	if truncated["communal"] {
		t.Fatal("пустой слой не усечён")
	}
}

func TestEcologyIsGoneAndCrimeIsAllowed(t *testing.T) {
	if AllowedLayers["ecology"] {
		t.Fatal("под ecology нет источника нигде — слой должен быть убран")
	}
	if !AllowedLayers["crime"] || !AllowedLayers["metro"] {
		t.Fatal("crime и metro должны быть разрешены")
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `cd backend && go test ./internal/service/ -run 'TestEvidence|TestEcology' -v`
Expected: FAIL — компиляция: `NewGeoLayersService` принимает один аргумент

- [ ] **Step 3: Реализовать репозиторий**

Создать `backend/internal/repository/evidence_repo.go`:

```go
// evidence_repo.go — READ-ONLY доступ к Python-owned таблице urban_evidence.
// Слои модельные (proxy), а не замеры, поэтому источник едет наружу вместе с
// геометрией: подпись происхождения — часть контракта, а не украшение.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type EvidenceRepo struct{ pool *pgxpool.Pool }

func NewEvidenceRepo(pool *pgxpool.Pool) *EvidenceRepo { return &EvidenceRepo{pool: pool} }

// ListByLayers возвращает упрощённую геометрию слоёв внутри bbox.
// Допуск 0.0001 ≈ 10 м на широте Москвы — тот же порядок, что у границ зон.
// limit+1 строк запрашивается намеренно: вызывающий по перебору узнаёт об усечении.
func (r *EvidenceRepo) ListByLayers(ctx context.Context, city string, layers []string,
	bbox [4]float64, limit int) ([]domain.EvidenceFeature, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT layer, source, weight, db,
		       ST_AsGeoJSON(ST_SimplifyPreserveTopology(geom, 0.0001), 5)
		FROM urban_evidence
		WHERE city = $1 AND layer = ANY($2)
		  AND geom && ST_MakeEnvelope($3, $4, $5, $6, 4326)
		LIMIT $7`,
		city, layers, bbox[0], bbox[1], bbox[2], bbox[3], limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.EvidenceFeature
	for rows.Next() {
		var f domain.EvidenceFeature
		if err := rows.Scan(&f.Layer, &f.Source, &f.Weight, &f.DB, &f.GeometryJSON); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
```

В `domain.go` добавить:

```go
// EvidenceFeature — строка urban_evidence с уже сериализованной геометрией.
type EvidenceFeature struct {
	Layer        string
	Source       string
	Weight       *float64
	DB           *float64
	GeometryJSON string
}
```

- [ ] **Step 4: Реализовать сервис**

В `geo_layers_service.go` заменить enum и конструктор, добавить ветку evidence:

```go
// AllowedLayers — закрытый enum из frontend/Пайплайн фронт.md §5.
// ecology убран: источника под экологию нет ни в poi, ни в urban_evidence
// (CHECK допускает только communal/crime/noise). crime, наоборот, уже импортирован.
var AllowedLayers = map[string]bool{
	"communal": true, "noise": true, "schools": true,
	"bars": true, "crime": true, "parks": true, "metro": true,
}

// Слои из urban_evidence. Все три — МОДЕЛЬНЫЕ прокси, не замеры, поэтому
// источник едет наружу в properties каждой фичи.
var evidenceLayers = map[string]bool{"communal": true, "noise": true, "crime": true}

const (
	evidenceFeatureLimit = 5000
	evidenceSourceLabel  = "source"
)

type evidenceLister interface {
	ListByLayers(ctx context.Context, city string, layers []string,
		bbox [4]float64, limit int) ([]domain.EvidenceFeature, error)
}

type GeoLayersService struct {
	pois     poiLister
	evidence evidenceLister
}

func NewGeoLayersService(pois poiLister, evidence evidenceLister) *GeoLayersService {
	return &GeoLayersService{pois: pois, evidence: evidence}
}
```

Сигнатура `Layers` меняется; тело POI-ветки остаётся прежним, добавляется evidence-ветка:

```go
func (s *GeoLayersService) Layers(ctx context.Context, city string, requested []string,
	bbox *[4]float64) (map[string]geojson.FeatureCollection, map[string]bool, error) {
	out := make(map[string]geojson.FeatureCollection)
	truncated := make(map[string]bool)

	var evidenceWanted []string
	var kindsToFetch []string
	layersNeedingKinds := map[string][]string{}

	for _, layer := range requested {
		if !AllowedLayers[layer] {
			continue
		}
		if evidenceLayers[layer] {
			// Без вьюпорта отдавать нечего: сырой слой шума — это 46 335 линий.
			// Пустой слой — честный ответ «нет данных», а не 500.
			if bbox == nil {
				out[layer] = geojson.NewFeatureCollection()
				continue
			}
			evidenceWanted = append(evidenceWanted, layer)
			out[layer] = geojson.NewFeatureCollection()
			continue
		}
		kinds, ok := layerKinds[layer]
		if !ok {
			out[layer] = geojson.NewFeatureCollection()
			continue
		}
		layersNeedingKinds[layer] = kinds
		kindsToFetch = append(kindsToFetch, kinds...)
	}

	if len(evidenceWanted) > 0 {
		rows, err := s.evidence.ListByLayers(ctx, city, evidenceWanted, *bbox, evidenceFeatureLimit)
		if err != nil {
			return nil, nil, err
		}
		perLayer := map[string]int{}
		for _, row := range rows {
			if perLayer[row.Layer] >= evidenceFeatureLimit {
				truncated[row.Layer] = true
				continue
			}
			perLayer[row.Layer]++
			props := map[string]any{"layer": row.Layer, evidenceSourceLabel: row.Source}
			if row.Weight != nil {
				props["weight"] = *row.Weight
			}
			if row.DB != nil {
				props["db"] = *row.DB
			}
			fc := out[row.Layer]
			fc.Features = append(fc.Features, geojson.Feature{
				Type: "Feature", Properties: props,
				Geometry: geojson.Geometry{Type: "", Coordinates: nil},
			})
			out[row.Layer] = fc
		}
	}
	// ... существующая POI-ветка без изменений ...
	return out, truncated, nil
}
```

Геометрия приходит из БД уже сериализованной строкой, поэтому в `geojson.go` нужен конструктор, который её принимает как есть:

```go
// RawGeometry оборачивает уже сериализованный GeoJSON из ST_AsGeoJSON —
// разбирать и пересобирать его на стороне Go незачем.
func RawFeature(geometryJSON string, props map[string]any) Feature {
	var g Geometry
	if err := json.Unmarshal([]byte(geometryJSON), &g); err != nil {
		return Feature{Type: "Feature", Properties: props}
	}
	return Feature{Type: "Feature", Properties: props, Geometry: g}
}
```

и в цикле выше вместо ручной сборки `geojson.Feature{...}` использовать `geojson.RawFeature(row.GeometryJSON, props)`.

- [ ] **Step 5: Реализовать хендлер и проводку**

`geo_handler.go`:

```go
// parseBbox разбирает "minLon,minLat,maxLon,maxLat" (EPSG:4326, порядок [lng,lat]).
// Неполный или неразбираемый bbox — это nil, а не ошибка: evidence-слой тогда
// вернётся пустым, как и любой слой без данных.
func parseBbox(raw string) *[4]float64 {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil
	}
	var box [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil
		}
		box[i] = v
	}
	return &box
}

func (h *GeoHandler) Layers(c *fiber.Ctx) error {
	raw := c.Query("layers")
	var requested []string
	if raw != "" {
		requested = strings.Split(raw, ",")
	}
	city := c.Query("city")
	if city == "" {
		city = "msk"
	}
	layers, truncated, err := h.layers.Layers(c.Context(), city, requested, parseBbox(c.Query("bbox")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"city": city, "layers": layers, "truncated": truncated})
}
```

`main.go` — создать репозиторий и передать вторым аргументом:

```go
	evidenceRepo := repository.NewEvidenceRepo(pool)
```
```go
	geoLayersService := service.NewGeoLayersService(poiRepo, evidenceRepo)
```

- [ ] **Step 6: Убедиться, что тесты проходят**

Run: `cd backend && go vet ./... && go test ./... 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 7: Коммит**

```bash
git add backend/internal/ backend/cmd/
git commit -m "feat: слои communal/noise/crime из urban_evidence с bbox и лимитом"
```

---

### Task 11: Фронт — слои, адрес, nullability, город

**Files:**
- Modify: `frontend/lib/agent/types.ts:18-27,43-54`, `lib/format.ts:1-3`, `lib/api/geo.ts:11-23`, `lib/store/session.ts:43-58,89`, `lib/map/useMaplibre.ts:19-40`, `components/result/PropertyCard.tsx:44-48`
- Test: `frontend/lib/format.test.ts`, `frontend/components/result/PropertyCard.test.tsx`, `frontend/lib/store/session.test.ts`

**Interfaces:**
- Consumes: `FinalResultObject.address` из Task 9; набор слоёв из Task 10.
- Produces: `LayerId = "communal" | "noise" | "schools" | "bars" | "crime" | "parks" | "metro"`; `Property.address: string`; `Property.price_from: number | null`.

- [ ] **Step 1: Написать падающие тесты**

В `frontend/lib/format.test.ts`:

```ts
it("не выдумывает цену, когда её нет", () => {
  // Go шлёт *int64 и присылает null; раньше money(null) рисовал «0 млн ₽»
  expect(money(null)).toBe("цена не указана");
  expect(money(undefined)).toBe("цена не указана");
  expect(money(44872500)).toBe("44.9 млн ₽");
});
```

В `frontend/components/result/PropertyCard.test.tsx` (фикстура в файле называется `PROPERTIES`, импортируется из `@/test/fixtures`):

```tsx
it("показывает реальный адрес вместо синтезированного имени", () => {
  const property = { ...PROPERTIES[0], address: "Москва, 2-й Донской проезд" };
  render(<PropertyCard property={property} index={0} onOpen={() => {}} />);
  expect(screen.getByText("Москва, 2-й Донской проезд")).toBeInTheDocument();
});

it("откатывается к имени, когда адреса нет", () => {
  const property = { ...PROPERTIES[0], address: "" };
  render(<PropertyCard property={property} index={0} onOpen={() => {}} />);
  expect(screen.getByText("ЖК Neva Residence")).toBeInTheDocument();
});
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `cd frontend && npx vitest run lib/format.test.ts components/result/PropertyCard.test.tsx`
Expected: FAIL — `expected '0 млн ₽' to be 'цена не указана'`

- [ ] **Step 3: Реализовать**

`types.ts`:

```ts
export type LayerId =
  | "communal" | "noise" | "schools" | "bars" | "crime" | "parks" | "metro";

export const LAYER_LABELS: Record<LayerId, string> = {
  communal: "Коммунальный фонд",
  noise: "Шум",
  schools: "Школы",
  bars: "Бары",
  crime: "Риск-зоны",
  parks: "Парки",
  metro: "Метро",
};
```

В `Property` заменить типы на честные и добавить адрес:

```ts
export interface Property {
  id: string;
  name: string;
  address: string;
  cover_image: string;
  match_score: number;
  price_from: number | null;
  rooms: number | null;
  area_sqm: number | null;
  floor: string;
  tags: string[];
  coordinates: [number, number];
}
```

`lib/format.ts`:

```ts
export function money(n: number | null | undefined): string {
  if (n === null || n === undefined) return "цена не указана";
  return Math.round((n / 1_000_000) * 10) / 10 + " млн ₽";
}
```

`PropertyCard.tsx` — показывать адрес строкой над ценой, оставив `name` фолбэком:

```tsx
        <h3 className="font-medium text-[15px] tracking-tight text-[#1c1d20]">
          {property.address || property.name}
        </h3>
```

`session.ts` — в `initial.activeLayers` заменить `ecology: false` на `crime: false` и добавить `metro: true`; в `setCity` сбросить кэш слоёв и выдачу:

```ts
  setCity: (city) => set({ city, layerData: {}, properties: [], zoneGeoJSON: null,
                           areaLabel: null, selectedIndex: 0 }),
```

`useMaplibre.ts` — реагировать на смену города отдельным эффектом (пересоздавать карту не нужно):

```ts
  useEffect(() => {
    const map = mapRef.current;
    if (!map || !ready) return;
    map.flyTo({ center: CITY_CENTER[city], zoom: 12.5, duration: 900 });
  }, [city, ready]);
```

`lib/api/geo.ts` — принимать и передавать `bbox`:

```ts
export async function fetchLayers(
  city: string, layers: LayerId[], bbox?: [number, number, number, number],
): Promise<LayerCollections> {
  if (!layers.length) return {};
  const bboxParam = bbox ? `&bbox=${bbox.join(",")}` : "";
  const res = await fetch(
    `${API_BASE}/geo/layers?city=${encodeURIComponent(city)}&layers=${layers.join(",")}${bboxParam}`,
    { credentials: "include" },
  );
  if (!res.ok) throw new Error(`fetchLayers failed: ${res.status}`);
  const body = await res.json();
  return (body.layers ?? {}) as LayerCollections;
}
```

`session.ts` — хранить вьюпорт и передавать его в загрузку слоя. Evidence-слои без bbox приходят пустыми, поэтому карта сообщает store свои границы:

В интерфейс `SessionState` добавить два члена, в `initial` — начальное значение:

```ts
  /** Границы вьюпорта карты [minLon, minLat, maxLon, maxLat] — нужны evidence-слоям. */
  viewport: [number, number, number, number] | null;
  setViewport: (b: [number, number, number, number]) => void;
```

```ts
  viewport: null as [number, number, number, number] | null,
```

```ts
  setViewport: (viewport) => set({ viewport }),

  // Слой тянется под текущий вьюпорт. Повторные вкл/выкл по сети не бьют,
  // но смена города сбрасывает кэш (см. setCity).
  loadLayer: async (id) => {
    if (get().layerData[id]) return;
    try {
      const fetched = await fetchLayers(get().city, [id], get().viewport ?? undefined);
      set((s) => ({ layerData: { ...s.layerData, ...fetched } }));
    } catch {
      // Слой не пришёл — карта просто его не покажет. Молча, без падения.
    }
  },
```

В `MapCanvas.tsx` в эффекте, который уже подписан на `moveend`, сообщать границы в store:

```tsx
  useEffect(() => {
    if (!map || !ready) return;
    const publish = () => {
      const b = map.getBounds();
      setViewport([b.getWest(), b.getSouth(), b.getEast(), b.getNorth()]);
    };
    publish();
    map.on("moveend", publish);
    return () => { map.off("moveend", publish); };
  }, [map, ready, setViewport]);
```

**`frontend/test/fixtures.ts` обязательно правится в этой же задаче**, иначе `tsc` упадёт в трёх местах: `PROPERTIES` — добавить `address` каждому из четырёх объектов; `LAYER_GEOJSON: Record<LayerId, GeoJSON.FeatureCollection>` — убрать ключ `ecology` и добавить `crime` и `metro`, иначе `Record` по новому `LayerId` станет неполным.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `cd frontend && npx tsc --noEmit && npm test && npm run lint`
Expected: tsc чисто, все тесты PASS, 0 errors в eslint

- [ ] **Step 5: Коммит**

```bash
git add frontend/
git commit -m "feat: адрес в карточке, слои crime и metro, переключатель города"
```

---

### Task 12: Честность прокси-слоёв и три градации шума

**Files:**
- Modify: `habitus/db/schema.sql:53-56` (комментарий), `habitus/geo/enrich.py` (noise), `habitus/online/dossier.py:459-476`, `backend/internal/service/display_fields.go:63-82` (`BuildTags`)
- Test: `tests/test_enrich.py`, `backend/internal/service/display_fields_test.go`

**Interfaces:**
- Consumes: `urban_evidence` слой `noise` (модельные дБ), `poi` (барный прокси).
- Produces: `noise_level ∈ {'low','medium','high'}`; `BuildTags` больше не печатает «шум: …» как факт.

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_enrich.py`:

```python
def test_noise_has_three_grades_from_evidence():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("DELETE FROM urban_evidence;")
            # модельные дБ рядом с тремя объектами
            for eid, lon, db in (("Q", 37.60, 45.0), ("M", 37.62, 60.0), ("L", 37.64, 70.0)):
                cur.execute("""INSERT INTO listings (external_id, source, geom, city)
                    VALUES (%s,'t',ST_SetSRID(ST_MakePoint(%s,55.75),4326),'msk');""",
                            (eid, lon))
                cur.execute("""INSERT INTO urban_evidence
                    (source_id, source, city, layer, geom, db, observed_at)
                    VALUES (%s,'test','msk','noise',
                            ST_SetSRID(ST_MakePoint(%s,55.75),4326),%s,now());""",
                            (eid, lon, db))
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT external_id, noise_level FROM listings ORDER BY external_id;")
            got = dict(cur.fetchall())
    assert got == {"L": "high", "M": "medium", "Q": "low"}
```

В `display_fields_test.go`:

```go
func TestBuildTagsDoesNotClaimMeasuredNoise(t *testing.T) {
	tags := BuildTags(map[string]any{"noise_level": "low", "bar_density_500m": 0.0})
	for _, tag := range tags {
		if strings.HasPrefix(tag, "шум:") {
			t.Fatalf("noise_level — прокси по барам, нельзя подавать как замер: %q", tag)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_enrich.py::test_noise_has_three_grades_from_evidence -v`
Expected: FAIL — `{'L': 'low', 'M': 'low', 'Q': 'low'}`, градации `medium` нет

- [ ] **Step 3: Реализовать**

В `_ENRICH_SQL` заменить блок `noise_level`. Пороги — обычная шкала транспортного шума: до 55 дБ тихо, 55–65 умеренно, выше — шумно. Барный прокси остаётся фолбэком там, где нет покрытия слоем:

```sql
  noise_level = COALESCE(
    (SELECT CASE WHEN avg(e.db) < 55 THEN 'low'
                 WHEN avg(e.db) < 65 THEN 'medium'
                 ELSE 'high' END
     FROM urban_evidence e
     WHERE e.city = l.city AND e.layer = 'noise'
       AND ST_DWithin(e.geom::geography, l.geom::geography, 500)),
    CASE WHEN (SELECT count(*) FROM poi p WHERE p.kind='bar' AND p.city = l.city
               AND ST_DWithin(l.geom::geography, p.geom::geography, 200)) > 2
         THEN 'high' ELSE 'low' END),
```

В `display_fields.go` в `BuildTags` заменить блок шума на то, что действительно измерено:

```go
	if v, ok := numFact(facts, "bar_density_500m"); ok {
		if v == 0 {
			tags = append(tags, "баров рядом нет")
		} else {
			tags = append(tags, fmt.Sprintf("баров рядом: %.0f", v))
		}
	}
```
(и убрать существующие ветки `bar_density_500m` и `noise_level`, чтобы не было дублей).

В `schema.sql` заменить комментарий над `urban_evidence` — сейчас он обещает наблюдение, которого нет:

```sql
-- Source-attributed evidence used by the dossier and the map layers.  Every
-- layer currently loaded is a MODEL, not a measurement: communal is derived
-- from OSM building start_date, crime from alcohol-outlet density, noise from
-- road classes.  `db` is therefore a modelled value, not an observed one — it
-- must always be published together with `source`.  Runtime code never
-- replaces absent values with zero.
```

В `dossier.py` дописать происхождение в `verdict_line`. В блоке `social_environment`:

```python
            verdict_line="Оценка по модельным слоям: коммунальность — по году постройки, риск — по плотности алкомаркетов.",
```

В блоке `view_and_climate`:

```python
            verdict_line="Свет рассчитан по геометрии зданий; шум — модель по типам дорог.",
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_enrich.py tests/test_dossier.py -v && cd backend && go test ./internal/service/ -v 2>&1 | tail -10`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/ backend/internal/service/ tests/
git commit -m "fix: три градации шума из слоя и честные подписи модельных источников"
```

---

### Task 13: Единый match_score

**Files:**
- Create: `backend/migrations/0008_match_score.up.sql`, `backend/migrations/0008_match_score.down.sql`
- Modify: `backend/internal/domain/domain.go:56-70`, `repository/chat_search_repo.go:38-90`, `service/search_stream_service.go:375-403`, `service/object_service.go:328-336,436-439`
- Test: `backend/internal/service/object_dossier_contract_test.go`

**Interfaces:**
- Consumes: `RescaleScore(score, rank, degraded) int` (существует).
- Produces: колонка `chat_search_results.match_score int`; `domain.ChatSearchResult.MatchScore int`. `RescaleScoreFromStored` удаляется.

- [ ] **Step 1: Написать падающий тест**

В `backend/internal/service/object_dossier_contract_test.go` добавить:

```go
func TestPassportScoreMatchesListScore(t *testing.T) {
	// Список считает RescaleScore(score, rank, degraded); паспорт раньше
	// пересчитывал из stored-скора без ранга и degraded — числа расходились.
	stored := RescaleScore(0.031, 2, []string{"reranker"})
	analysis := fallbackAnalysis(stored, "", map[string]any{})
	if analysis.MatchScore != stored {
		t.Fatalf("паспорт показывает %d, список — %d", analysis.MatchScore, stored)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `cd backend && go test ./internal/service/ -run TestPassportScoreMatchesListScore -v`
Expected: FAIL — `паспорт показывает 3, список — 85`

- [ ] **Step 3: Реализовать**

`backend/migrations/0008_match_score.up.sql`:

```sql
ALTER TABLE chat_search_results ADD COLUMN match_score INT NOT NULL DEFAULT 0;
```

`backend/migrations/0008_match_score.down.sql`:

```sql
ALTER TABLE chat_search_results DROP COLUMN match_score;
```

В `domain.ChatSearchResult` добавить поле:

```go
	MatchScore       int
```

В `chat_search_repo.UpsertResult` — колонка в INSERT и в `DO UPDATE SET`:

```go
		INSERT INTO chat_search_results(chat_id, external_id, search_id, price, area,
		                                rooms, address_facts, score, match_score, explanation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (chat_id, external_id) DO UPDATE SET
		    search_id = EXCLUDED.search_id,
		    price = EXCLUDED.price,
		    area = EXCLUDED.area,
		    rooms = EXCLUDED.rooms,
		    address_facts = EXCLUDED.address_facts,
		    score = EXCLUDED.score,
		    match_score = EXCLUDED.match_score,
		    explanation = EXCLUDED.explanation,
		    dossier = NULL,
		    dossier_version = NULL,
		    dossier_updated_at = NULL,
		    updated_at = now()`,
		res.ChatID, res.ExternalID, res.SearchID, res.Price, res.Area, res.Rooms,
		factsJSON, res.Score, res.MatchScore, res.Explanation)
```

и в `GetResult` — `match_score` в SELECT и `&res.MatchScore` в `Scan` (порядок должен совпадать).

В `search_stream_service`: `buildFinalResult` возвращает уже собранные объекты, поэтому третьим значением отдать карту посчитанных процентов, а `persist` — принять её:

```go
	scores := make(map[string]int, len(objects))
	for _, o := range objects {
		scores[o.ID] = o.MatchScore
	}
	return FinalResultEvent{...}, objectIDs, scores
```

```go
		_ = s.searches.UpsertResult(ctx, domain.ChatSearchResult{
			ChatID: chatID, ExternalID: item.ExternalID, SearchID: searchID,
			Price: item.Price, Area: item.Area, Rooms: item.Rooms,
			AddressFacts: item.AddressFacts, Score: item.Score,
			MatchScore: scores[item.ExternalID], Explanation: resp.Explanation,
		})
```

В `object_service.go` — `fallbackAnalysis` принимает готовый процент, а не сырой скор:

```go
func fallbackAnalysis(matchScore int, summary string, facts map[string]any) LifestyleAnalysis {
	return LifestyleAnalysis{
		MatchScore: matchScore, Summary: summary,
```

вызов в `GetPassport` — `analysis := fallbackAnalysis(res.MatchScore, res.Explanation, res.AddressFacts)`. Функцию `RescaleScoreFromStored` удалить целиком: она и была источником расхождения.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `cd backend && go vet ./... && go test ./... 2>&1 | tail -20`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add backend/
git commit -m "fix: единый match_score в выдаче и паспорте объекта"
```

---

## Финальная проверка

- [ ] Полный прогон трёх стеков

```bash
docker compose up -d db
uv run pytest -q
cd backend && go vet ./... && go test ./... && cd ..
cd frontend && npx tsc --noEmit && npm test && npm run lint && cd ..
```

- [ ] Рабочая база цела после тестов

```bash
docker exec habitus-db-1 psql -U habitus -d habitus -tAc \
  "SELECT 'listings=' || count(*) FROM listings;"
```

- [ ] Дым вручную: `docker compose up`, войти, запрос «двушка в Хамовниках рядом с метро» — в карточках виден адрес, чип зоны, слой метро на карте, тумблеры communal/noise/crime показывают геометрию под вьюпортом.
