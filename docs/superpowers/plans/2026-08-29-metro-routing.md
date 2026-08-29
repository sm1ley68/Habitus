# Метро, МЦК и МЦД: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Дать пользователю без машины честное время «от двери до двери» на метро, МЦК и МЦД — как фильтр в подборе, как объяснённый маршрут в досье и как схему поездки на экране, для Москвы и Петербурга.

**Architecture:** Топология и геометрия линий приходят из OSM Overpass в новые таблицы графа; времена перегонов и пересадок — из курируемого JSON в репо, с помеченной фолбэк-оценкой там, где пары нет. Граф грузится в память ML-сервиса и обходится Дейкстрой: один и тот же обход даёт и число для SQL-фильтра, и разбивку маршрута для отрисовки.

**Tech Stack:** Python 3 / FastAPI / psycopg 3 / PostGIS, Go / Fiber / pgx, Next.js / React / TypeScript / vitest, OSM Overpass, OpenRouteService.

**Spec:** `docs/superpowers/specs/2026-08-29-metro-routing-design.md`

## Global Constraints

- Работа идёт напрямую в `main`. Отдельные ветки не заводить.
- Коммиты — Conventional Commits **на русском**: `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`. **Никаких трейлеров и подписей**, в том числе `Co-Authored-By`.
- Координаты **везде** `[lng, lat]`, WGS84 (EPSG:4326). Без трансформаций на фронте.
- Enum'ы синхронны на трёх сторонах: `habitus/online/schema.py` ↔ `backend/internal/service/` ↔ `frontend/lib/agent/types.ts`.
- **Синтетический ноль вместо отсутствующего замера запрещён.** Нет данных — блок деградирует до отсутствия, а не показывает нули.
- Каждое время несёт признак происхождения: курированное значение и оценка по расстоянию различимы структурно вплоть до фронта (`estimated: bool`).
- `walk_min_metro` и фильтр `geo: [{kind: "metro"}]` остаются **только про подземку**: платформы МЦК и МЦД туда не подмешиваются.
- Секреты не коммитить. Новых обязательных ключей окружения план не вводит.
- Тесты: Python `uv run pytest`, Go `cd backend && go test ./...`, фронт `cd frontend && npm test`.
- Python-тесты с БД скипаются без поднятого Postgres, а не падают. Новые БД-тесты обязаны следовать этому же правилу — через фикстуру `conn`, открывающую `psycopg.connect(settings.db_dsn)` (обёртка в `tests/conftest.py` сама превратит недоступную БД в `skip`).
- Значения времён (`headway_s`, `fallback_speed_kmh`, секунды перегонов) — **данные, а не логика**: живут в `data/metro/*.json` и в колонках БД, не в константах кода.

---

## Этапы

| Этап | Задачи | Что работает по завершении |
|------|--------|----------------------------|
| A. Данные | 1–8 | Граф трёх систем двух городов лежит в БД, у объектов есть пешие плечи до платформ, всё собирается одной командой |
| B. Движок и контракт | 9–11 | Маршрут считается, контракт описан, досье показывает метро-ногу |
| C. Поиск | 12–13 | «40 минут до работы на метро» фильтрует выдачу |
| D. Go-шлюз | 14–15 | Новые поля и линии метро доезжают до фронта |
| E. Фронт | 16–18 | Лента-схема и линии на карте |
| F. Раскатка | 19 | Граф собран на живых данных, метрики перемерены и записаны |

Этапы A→B→C и A→B→D→E последовательны; Задача 19 идёт последней. Задачи 14–15 не зависят от 12–13 и могут идти параллельно.

---

## Файловая структура

**Создаются:**

| Файл | Ответственность |
|------|-----------------|
| `habitus/geo/metro.py` | Запрос к Overpass и разбор релейшенов маршрутов в строки таблиц |
| `habitus/geo/metro_times.py` | Загрузка курируемого JSON, нормализация имён, фолбэк-оценка |
| `habitus/geo/metro_access.py` | Пешие плечи «объект → станция», обновление `walk_min_metro` |
| `habitus/online/metro_route.py` | Граф в памяти, Дейкстра, SQL-предикат для поиска |
| `data/metro/msk.json`, `data/metro/spb.json` | Курируемые времена |
| `tests/fixtures/overpass_subway_msk.json` | Зафиксированный ответ Overpass для тестов |
| `tests/test_metro_parse.py`, `tests/test_metro_times.py`, `tests/test_metro_route.py`, `tests/test_metro_access_db.py`, `tests/test_metro_search_db.py` | Тесты |
| `backend/internal/repository/metro_repo.go` | Read-only доступ к таблицам графа |
| `frontend/components/passport/viz/MetroRouteStrip.tsx` | Лента-схема поездки |
| `frontend/components/passport/viz/MetroRouteStrip.test.tsx` | Тесты ленты |
| `docs/notes/osm-transit-tags-2026-08-29.md` | Протокол разведки тегов МЦК/МЦД |

**Модифицируются:** `habitus/geo/osm_extract.py`, `habitus/geo/enrich.py`, `habitus/db/schema.sql`, `habitus/cli.py`, `habitus/online/schema.py`, `habitus/online/dossier.py`, `habitus/online/orchestrator.py`, `habitus/online/nlu.py`, `backend/internal/service/object_service.go`, `backend/internal/service/geo_layers_service.go`, `frontend/lib/agent/types.ts`, `frontend/components/passport/viz/index.ts`, `frontend/components/passport/viz/FamilyDayGraph.tsx`.

---

# Этап A — данные

## Task 1: Разведка тегов МЦК и МЦД в OSM

Спека прямо запрещает фиксировать эти теги по памяти: разметка у диаметров менялась. Задача исследовательская, её результат — протокол и фикстура, на которые опираются все следующие задачи.

**Files:**
- Create: `docs/notes/osm-transit-tags-2026-08-29.md`
- Create: `tests/fixtures/overpass_subway_msk.json`

**Interfaces:**
- Consumes: ничего.
- Produces: файл фикстуры `tests/fixtures/overpass_subway_msk.json` — сырой JSON-ответ Overpass с элементами `relation`, каждый с `tags` и `members`. Задача 3 разбирает именно его. Протокол фиксирует три строки Overpass-фильтра, которые Задача 2 положит в `TRANSIT_RELATION_FILTER`.

- [ ] **Step 1: Запросить релейшены метро Москвы и сохранить как фикстуру**

```bash
mkdir -p tests/fixtures
curl -s -X POST https://overpass-api.de/api/interpreter \
  -H 'User-Agent: Habitus/1.0 (real-estate research)' \
  --data-urlencode 'data=[out:json][timeout:180];relation["route"="subway"](55.00,36.60,56.30,38.60);out tags;' \
  -o tests/fixtures/overpass_subway_msk_tags.json
python3 -c "import json;d=json.load(open('tests/fixtures/overpass_subway_msk_tags.json'));print(len(d['elements']));[print(e['id'],e['tags'].get('ref'),e['tags'].get('name'),e['tags'].get('colour')) for e in d['elements'][:40]]"
```

Ожидается: несколько десятков релейшенов (по два на линию — по направлению), с `ref` вида `1`…`15` и `colour` вида `#EF161E`.

- [ ] **Step 2: Найти, как размечены МЦК и МЦД**

```bash
for q in 'relation["route"="light_rail"](55.00,36.60,56.30,38.60);' \
         'relation["route"="train"]["ref"~"^D[1-5]$"](55.00,36.60,56.30,38.60);' \
         'relation["route"="train"]["name"~"МЦД"](55.00,36.60,56.30,38.60);' \
         'relation["route"="train"]["network"~"МЦД|MCD"](55.00,36.60,56.30,38.60);'; do
  echo "=== $q"
  curl -s -X POST https://overpass-api.de/api/interpreter \
    -H 'User-Agent: Habitus/1.0 (real-estate research)' \
    --data-urlencode "data=[out:json][timeout:180];${q}out tags;" \
  | python3 -c "import json,sys;d=json.load(sys.stdin);print('элементов:',len(d['elements']));[print(' ',e['id'],e['tags'].get('ref'),'|',e['tags'].get('name'),'|',e['tags'].get('network'),'|',e['tags'].get('colour')) for e in d['elements'][:15]]"
  sleep 5
done
```

- [ ] **Step 3: Записать протокол**

Создать `docs/notes/osm-transit-tags-2026-08-29.md` со структурой: дата, точная команда каждого запроса, число элементов, 5–10 примеров тегов, и **вывод** — по одной строке Overpass-фильтра на каждую из трёх систем, которую можно скопировать в код. Обязательно зафиксировать, у каких линий `colour` отсутствует: от этого зависит фолбэк палитры на фронте.

Если ни один вариант для МЦД не дал осмысленной выдачи — записать это как факт и указать выбранный запасной признак отбора. Модель данных от конкретных тегов не зависит, правится только запрос.

- [ ] **Step 4: Сохранить полную фикстуру с members для одной линии**

```bash
curl -s -X POST https://overpass-api.de/api/interpreter \
  -H 'User-Agent: Habitus/1.0 (real-estate research)' \
  --data-urlencode 'data=[out:json][timeout:180];relation["route"="subway"]["ref"="1"](55.00,36.60,56.30,38.60);out body geom;' \
  -o tests/fixtures/overpass_subway_msk.json
python3 -c "import json;d=json.load(open('tests/fixtures/overpass_subway_msk.json'));print('элементов:',len(d['elements']));e=[x for x in d['elements'] if x['type']=='relation'][0];print('роли:',sorted({m['role'] for m in e['members']}))"
```

Ожидается: роли содержат `stop` и/или `platform` плюс пустую роль у ways трассы.

- [ ] **Step 5: Ужать фикстуру до разумного размера**

Файл с полной геометрией может весить мегабайты. Оставить релейшен, все его `node`-members и первые 3 `way` трассы:

```bash
python3 - <<'EOF'
import json
p = "tests/fixtures/overpass_subway_msk.json"
d = json.load(open(p))
rel = next(x for x in d["elements"] if x["type"] == "relation")
nodes = [x for x in d["elements"] if x["type"] == "node"]
ways = [x for x in d["elements"] if x["type"] == "way"][:3]
keep_ids = {w["id"] for w in ways} | {n["id"] for n in nodes}
rel["members"] = [m for m in rel["members"] if m["ref"] in keep_ids]
json.dump({"elements": [rel] + nodes + ways}, open(p, "w"), ensure_ascii=False)
print("станций в фикстуре:", sum(1 for m in rel["members"] if m["type"] == "node"))
EOF
```

- [ ] **Step 6: Commit**

```bash
git add docs/notes/osm-transit-tags-2026-08-29.md tests/fixtures/
git commit -m "docs: протокол разведки тегов МЦК и МЦД в OSM и фикстура Overpass"
```

---

## Task 2: bbox по городам в osm_extract

Сейчас `MSK_AREA` зашита в модуле и подставляется во все запросы, поэтому весь сбор POI московский по построению.

**Files:**
- Modify: `habitus/geo/osm_extract.py:9-33` (константа `MSK_AREA`, словарь `OVERPASS_QUERIES`, функция `fetch_kind`)
- Modify: `habitus/cli.py:57-63` (цикл сбора POI)
- Test: `tests/test_osm_extract.py`

**Interfaces:**
- Consumes: ничего.
- Produces:
  - `CITY_AREA: dict[str, str]` — bbox городов в формате Overpass `(south,west,north,east)`, ключи `"msk"` / `"spb"`.
  - `TRANSIT_AREA: dict[str, str]` — расширенный bbox для транспортного графа.
  - `overpass_queries(area: str) -> dict[str, str]` — словарь `kind → фрагмент запроса` для данного bbox.
  - `fetch_kind(kind: str, city: str = "msk", http_post=requests.post, retries: int = 4, backoff: float = 3.0) -> list[dict]` — сигнатура получает второй позиционный параметр `city`.
  - `POI_KINDS: tuple[str, ...]` — имена слоёв POI, заменяет обход `OVERPASS_QUERIES` в `cli.py`.

- [ ] **Step 1: Написать падающий тест**

Добавить в `tests/test_osm_extract.py`:

```python
from habitus.geo.osm_extract import (CITY_AREA, POI_KINDS, TRANSIT_AREA,
                                     fetch_kind, overpass_queries)


def test_city_area_covers_both_cities():
    assert set(CITY_AREA) == {"msk", "spb"}
    # формат Overpass: (south,west,north,east)
    assert CITY_AREA["spb"] == "(59.70,29.60,60.20,30.70)"


def test_transit_area_for_moscow_is_wider_than_city():
    # диаметры уходят далеко за город; городской bbox рвал бы линию посередине
    city = [float(x) for x in CITY_AREA["msk"].strip("()").split(",")]
    transit = [float(x) for x in TRANSIT_AREA["msk"].strip("()").split(",")]
    assert transit[0] < city[0] and transit[1] < city[1]
    assert transit[2] > city[2] and transit[3] > city[3]


def test_transit_area_for_spb_equals_city_area():
    # диаметров в Петербурге нет — расширять нечего
    assert TRANSIT_AREA["spb"] == CITY_AREA["spb"]


def test_queries_are_built_for_the_requested_city():
    q = overpass_queries(CITY_AREA["spb"])
    assert set(q) == set(POI_KINDS)
    assert all(CITY_AREA["spb"] in fragment for fragment in q.values())


def test_fetch_kind_sends_the_city_bbox():
    seen = {}

    class Resp:
        status_code = 200
        def raise_for_status(self): pass
        def json(self): return {"elements": []}

    def fake_post(url, data=None, headers=None, timeout=None):
        seen["data"] = data["data"]
        return Resp()

    fetch_kind("metro", "spb", http_post=fake_post)
    assert CITY_AREA["spb"] in seen["data"]
    assert CITY_AREA["msk"] not in seen["data"]
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_osm_extract.py -v`
Expected: FAIL — `ImportError: cannot import name 'CITY_AREA'`

- [ ] **Step 3: Реализовать**

В `habitus/geo/osm_extract.py` заменить блок константы и словаря запросов:

```python
# bbox в формате Overpass: (south,west,north,east). Зеркало CITY_BBOX из
# habitus/clean/normalize.py и frontend/lib/city.ts — там порядок другой
# ([lng_min, lat_min, lng_max, lat_max]), сверять по значениям, не по позициям.
CITY_AREA = {
    "msk": "(55.48,37.30,55.95,37.95)",
    "spb": "(59.70,29.60,60.20,30.70)",
}

# Транспортный bbox шире городского: МЦД уходят далеко за Москву (D1 до
# Одинцова и Лобни, D3 от Зеленограда до Раменского), и городской bbox рвал бы
# линию посередине — граф получился бы несвязным. Объявления в этот bbox не
# попадают: он используется ИСКЛЮЧИТЕЛЬНО построением транспортного графа.
TRANSIT_AREA = {
    "msk": "(55.00,36.60,56.30,38.60)",
    "spb": CITY_AREA["spb"],   # диаметров нет — расширять нечего
}

POI_KINDS = ("school", "bar", "alcohol", "park", "metro")


def overpass_queries(area: str) -> dict[str, str]:
    """Фрагменты Overpass-запросов по слоям POI для конкретного bbox."""
    return {
        # Школьные здания в OSM — way/relation, а не node: node-only запрос давал
        # 173 школы на Москву вместо ~1500, и walk_min_school врал.
        "school":  f'(node["amenity"="school"]{area};'
                   f'way["amenity"="school"]{area};'
                   f'relation["amenity"="school"]{area};);',
        "bar":     f'node["amenity"~"bar|pub"]{area};',
        "alcohol": f'node["shop"="alcohol"]{area};',
        # парки в OSM — полигоны (way/relation), а не точки; берём и их центроид.
        "park":    f'(node["leisure"="park"]{area};'
                   f'way["leisure"="park"]{area};'
                   f'relation["leisure"="park"]{area};);',
        "metro":   f'node["station"="subway"]{area};',
    }
```

Убрать старые `MSK_AREA` и `OVERPASS_QUERIES`; `URBAN_FEATURE_QUERY` перевести на `CITY_AREA["msk"]` — он остаётся московским (урбан-фичи в скоуп этой спеки не входят), но перестаёт зависеть от удалённой константы:

```python
_MSK = CITY_AREA["msk"]
URBAN_FEATURE_QUERY = (
    f'(way["building"]{_MSK};'
    f'way["leisure"="park"]{_MSK};'
    f'way["natural"="water"]{_MSK};'
    f'way["waterway"="riverbank"]{_MSK};);'
)
```

`fetch_kind` получает город:

```python
def fetch_kind(kind: str, city: str = "msk", http_post=requests.post,
               retries: int = 4, backoff: float = 3.0) -> list[dict]:
    # POST надёжнее GET на крупных запросах; [timeout:120] — серверный лимит Overpass.
    # `out center;` — для way/relation отдаёт центроид, для node просто координаты.
    q = f"[out:json][timeout:120];{overpass_queries(CITY_AREA[city])[kind]}out center;"
    last = ""
    for attempt in range(retries):
        try:
            r = http_post(OVERPASS_URL, data={"data": q}, headers=HEADERS,
                          timeout=180)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return parse_overpass(kind, r.json())
        except requests.exceptions.RequestException as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass '{kind}' не удался за {retries} попыток: {last}")
```

- [ ] **Step 4: Поправить вызывающий код в cli.py**

В `habitus/cli.py` заменить импорт `OVERPASS_QUERIES` на `POI_KINDS` и цикл сбора (строки 57–63) на:

```python
    stats["osm_failed"] = []
    if fetch_osm:
        for kind in POI_KINDS:
            try:
                upsert_poi(fetch_kind(kind, city), conn, city=city)
            except Exception as e:  # noqa: BLE001 — внешний API, причин отказа много
                conn.rollback()
                stats["osm_failed"].append(f"{kind}/{city}: {e}")
```

Соответственно `run_offline` получает параметр `city: str = "msk"` в сигнатуре, а парсер `offline` в `main()` — аргумент:

```python
    off.add_argument("--city", choices=["msk", "spb"], default="msk")
```

и передачу `city=args.city` в вызов `run_offline`.

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_osm_extract.py tests/test_cli_smoke.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/geo/osm_extract.py habitus/cli.py tests/test_osm_extract.py
git commit -m "refactor: параметризовать bbox сбора POI по городу"
```

---

## Task 3: Разбор релейшенов маршрутов

Чистая функция разбора — без сети и без БД, на зафиксированной фикстуре из Задачи 1.

**Files:**
- Create: `habitus/geo/metro.py`
- Create: `tests/test_metro_parse.py`

**Interfaces:**
- Consumes: `CITY_AREA`, `TRANSIT_AREA` из Задачи 2; фикстуру `tests/fixtures/overpass_subway_msk.json` из Задачи 1.
- Produces:
  - `@dataclass StationRaw(osm_id: int, name: str, lon: float, lat: float)`
  - `@dataclass LineRaw(system: str, ref: str, name: str, colour: str | None, stations: list[StationRaw], geometry: list[list[float]])`
  - `normalize_station_name(name: str) -> str`
  - `parse_route_relations(payload: dict, system: str) -> list[LineRaw]`
  - `TRANSIT_RELATION_FILTER: dict[str, str]` — по одному Overpass-фильтру на систему.
  - `SYSTEMS: tuple[str, ...]` = `("subway", "mck", "mcd")`

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_parse.py`:

```python
import json
from pathlib import Path

import pytest

from habitus.geo.metro import (SYSTEMS, LineRaw, normalize_station_name,
                               parse_route_relations)

FIXTURE = Path(__file__).parent / "fixtures" / "overpass_subway_msk.json"


@pytest.fixture
def payload() -> dict:
    return json.loads(FIXTURE.read_text())


def test_systems_cover_all_three():
    assert SYSTEMS == ("subway", "mck", "mcd")


def test_parses_line_identity(payload):
    lines = parse_route_relations(payload, "subway")
    assert lines, "фикстура должна давать хотя бы одну линию"
    line = lines[0]
    assert isinstance(line, LineRaw)
    assert line.system == "subway"
    assert line.ref
    assert line.colour is None or line.colour.startswith("#")


def test_stations_keep_relation_order(payload):
    line = parse_route_relations(payload, "subway")[0]
    assert len(line.stations) >= 2
    # порядок следования — это порядок members релейшена, а не сортировка
    ids = [s.osm_id for s in line.stations]
    assert ids == list(dict.fromkeys(ids)), "дубликаты станций не допускаются"
    assert all(s.name for s in line.stations), "безымянная станция — мусор"
    assert all(-180 <= s.lon <= 180 and -90 <= s.lat <= 90 for s in line.stations)


def test_geometry_is_lng_lat_pairs(payload):
    line = parse_route_relations(payload, "subway")[0]
    assert all(len(p) == 2 for p in line.geometry)
    # [lng, lat]: в Москве долгота ~37, широта ~55 — перепутанный порядок видно сразу
    lng, lat = line.geometry[0]
    assert 30 < lng < 45 and 50 < lat < 60


def test_relation_without_stops_is_skipped():
    payload = {"elements": [
        {"type": "relation", "id": 1, "tags": {"route": "subway", "ref": "9"},
         "members": []},
    ]}
    assert parse_route_relations(payload, "subway") == []


@pytest.mark.parametrize("raw,expected", [
    ("Охотный Ряд", "охотный ряд"),
    ("охотный  ряд", "охотный ряд"),
    ("Тёплый Стан", "теплый стан"),
    ("Теплый Стан", "теплый стан"),
    ("Улица 1905 года", "улица 1905 года"),
    ("Библиотека имени Ленина", "библиотека имени ленина"),
    ("Ховрино ", "ховрино"),
    ("Петровско-Разумовская", "петровско-разумовская"),
])
def test_name_normalization(raw, expected):
    assert normalize_station_name(raw) == expected
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_parse.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'habitus.geo.metro'`

- [ ] **Step 3: Реализовать**

Создать `habitus/geo/metro.py`:

```python
# habitus/geo/metro.py — рельсовый транспорт из OSM: метро, МЦК, МЦД.
#
# Точные Overpass-фильтры взяты из разведки, запротоколированной в
# docs/notes/osm-transit-tags-2026-08-29.md. Разметка у МЦК и МЦД менялась,
# поэтому фиксировать её по памяти нельзя: при расхождении правится ФИЛЬТР,
# модель данных к конкретным тегам не привязана.
import re
import time
from dataclasses import dataclass, field

import requests

from habitus.geo.osm_extract import HEADERS, OVERPASS_URL, RETRY_STATUS, TRANSIT_AREA

SYSTEMS = ("subway", "mck", "mcd")

# Значения подставляются из протокола разведки (Задача 1, шаг 3).
TRANSIT_RELATION_FILTER = {
    "subway": 'relation["route"="subway"]',
    "mck":    'relation["route"="light_rail"]["ref"="14"]',
    "mcd":    'relation["route"="train"]["ref"~"^D[1-5]$"]',
}

# Роли members, которыми в OSM размечена остановка. platform идёт вторым:
# у части линий проставлен только он.
_STOP_ROLES = ("stop", "platform")


@dataclass
class StationRaw:
    osm_id: int
    name: str
    lon: float
    lat: float


@dataclass
class LineRaw:
    system: str
    ref: str
    name: str
    colour: str | None
    stations: list[StationRaw] = field(default_factory=list)
    geometry: list[list[float]] = field(default_factory=list)


def normalize_station_name(name: str) -> str:
    """Ключ сопоставления станции с курируемым JSON.

    Ключ по ИМЕНИ, а не по osm_id: правка разметки в OSM не должна обнулять
    курированные времена. Схлопываем регистр, ё→е, повторные пробелы и
    кавычки — ровно те различия, которыми одна и та же станция пишется
    по-разному в OSM и в курируемом файле.
    """
    s = name.strip().lower().replace("ё", "е")
    s = s.replace("«", "").replace("»", "").replace('"', "")
    return re.sub(r"\s+", " ", s)


def parse_route_relations(payload: dict, system: str) -> list[LineRaw]:
    """Ответ Overpass (`out body geom`) → линии со станциями в порядке следования."""
    nodes = {e["id"]: e for e in payload.get("elements", []) if e.get("type") == "node"}
    ways = {e["id"]: e for e in payload.get("elements", []) if e.get("type") == "way"}
    lines: list[LineRaw] = []

    for el in payload.get("elements", []):
        if el.get("type") != "relation":
            continue
        tags = el.get("tags") or {}
        stations: list[StationRaw] = []
        seen: set[int] = set()
        geometry: list[list[float]] = []

        for m in el.get("members", []):
            if m.get("type") == "node" and m.get("role") in _STOP_ROLES:
                node = nodes.get(m["ref"])
                if node is None:
                    continue
                nm = (node.get("tags") or {}).get("name")
                # Безымянная станция — мусор: подписать её на схеме нечем.
                if not nm or node["id"] in seen:
                    continue
                seen.add(node["id"])
                stations.append(StationRaw(osm_id=node["id"], name=nm,
                                           lon=node["lon"], lat=node["lat"]))
            elif m.get("type") == "way" and not m.get("role"):
                way = ways.get(m["ref"])
                for p in (way or {}).get("geometry") or []:
                    geometry.append([p["lon"], p["lat"]])

        # Релейшен без остановок описывает трассу, а не маршрут — линией он быть
        # не может: ни одной станции для графа из него не извлечь.
        if len(stations) < 2:
            continue
        lines.append(LineRaw(
            system=system,
            ref=tags.get("ref") or tags.get("name") or str(el["id"]),
            name=tags.get("name") or tags.get("ref") or str(el["id"]),
            colour=tags.get("colour"),
            stations=stations, geometry=geometry))
    return lines


def fetch_system(system: str, city: str, http_post=requests.post,
                 retries: int = 4, backoff: float = 3.0) -> list[LineRaw]:
    """Линии одной системы для города. Ретраи — как у fetch_kind: публичный
    Overpass под нагрузкой регулярно отдаёт транзиентные 429/502/503/504."""
    q = (f"[out:json][timeout:300];"
         f"{TRANSIT_RELATION_FILTER[system]}{TRANSIT_AREA[city]};out body geom;")
    last = ""
    for attempt in range(retries):
        try:
            r = http_post(OVERPASS_URL, data={"data": q}, headers=HEADERS,
                          timeout=360)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return parse_route_relations(r.json(), system)
        except requests.exceptions.RequestException as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass '{system}/{city}' не удался за {retries} "
                       f"попыток: {last}")
```

- [ ] **Step 4: Сверить фильтры с протоколом разведки**

Открыть `docs/notes/osm-transit-tags-2026-08-29.md` и подставить в `TRANSIT_RELATION_FILTER` те строки, которые записаны в выводе протокола. Значения в коде выше — ожидаемые, а не проверенные.

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_metro_parse.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/geo/metro.py tests/test_metro_parse.py
git commit -m "feat: разбор релейшенов метро, МЦК и МЦД из Overpass"
```

---

## Task 4: Курируемые времена и фолбэк-оценка

**Files:**
- Create: `habitus/geo/metro_times.py`
- Create: `data/metro/msk.json`, `data/metro/spb.json`
- Create: `tests/test_metro_times.py`

**Interfaces:**
- Consumes: `normalize_station_name` из Задачи 3.
- Produces:
  - `@dataclass CuratedTimes` с полями `headways: dict[str, int]`, `speeds: dict[str, float]`, `edges: dict[tuple[str, str, str], int]`, `transfers: dict[tuple[str, str], int]`, `outdoor: set[tuple[str, str]]`
  - `load_curated(city: str, data_dir: Path | None = None) -> CuratedTimes`
  - `edge_seconds(curated, line_ref, a_name, b_name, system, metres) -> tuple[int, bool]` — возвращает `(секунды, estimated)`
  - `transfer_seconds(curated, a_name, b_name) -> tuple[int, bool, bool]` — возвращает `(секунды, estimated, outdoor)`
  - `DEFAULT_TRANSFER_S: int = 180`, `STOP_DWELL_S: int = 25`

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_times.py`:

```python
import json

import pytest

from habitus.geo.metro_times import (DEFAULT_TRANSFER_S, edge_seconds,
                                     load_curated, transfer_seconds)


@pytest.fixture
def curated(tmp_path):
    (tmp_path / "metro").mkdir()
    (tmp_path / "metro" / "msk.json").write_text(json.dumps({
        "lines": [
            {"ref": "1", "system": "subway", "headway_s": 120,
             "fallback_speed_kmh": 40},
            {"ref": "D1", "system": "mcd", "headway_s": 600,
             "fallback_speed_kmh": 55},
        ],
        "edges": [
            {"line": "1", "from": "Сокольники", "to": "Красносельская",
             "seconds": 150},
        ],
        "transfers": [
            {"from": "Охотный Ряд", "to": "Театральная", "seconds": 180},
            {"from": "Площадь Гагарина", "to": "Ленинский проспект",
             "seconds": 420, "outdoor": True},
        ],
    }, ensure_ascii=False), encoding="utf-8")
    return load_curated("msk", tmp_path)


def test_headway_and_speed_come_from_data_not_code(curated):
    assert curated.headways["1"] == 120
    assert curated.headways["D1"] == 600
    assert curated.speeds["D1"] == 55.0


def test_curated_edge_is_used_verbatim(curated):
    seconds, estimated = edge_seconds(curated, "1", "Сокольники",
                                      "Красносельская", "subway", metres=1800)
    assert (seconds, estimated) == (150, False)


def test_curated_edge_matches_regardless_of_spelling(curated):
    # ё/е, регистр и лишние пробелы не должны рвать сопоставление
    seconds, estimated = edge_seconds(curated, "1", "СОКОЛЬНИКИ ",
                                      "красносельская", "subway", metres=1800)
    assert (seconds, estimated) == (150, False)


def test_missing_edge_falls_back_to_distance_and_is_marked(curated):
    # 2200 м на 40 км/ч = 198 с + стоянка; главное — пометка estimated
    seconds, estimated = edge_seconds(curated, "1", "Красносельская",
                                      "Комсомольская", "subway", metres=2200)
    assert estimated is True
    assert 180 < seconds < 260


def test_fallback_uses_the_lines_own_speed(curated):
    slow, _ = edge_seconds(curated, "1", "A", "B", "subway", metres=5000)
    fast, _ = edge_seconds(curated, "D1", "A", "B", "mcd", metres=5000)
    # у диаметров перегонная скорость выше — одна константа на всех врала бы
    assert fast < slow


def test_curated_transfer_carries_outdoor_flag(curated):
    seconds, estimated, outdoor = transfer_seconds(
        curated, "Площадь Гагарина", "Ленинский проспект")
    assert (seconds, estimated, outdoor) == (420, False, True)


def test_transfer_is_symmetric(curated):
    assert transfer_seconds(curated, "Театральная", "Охотный Ряд") == (180, False, False)


def test_unknown_transfer_falls_back_and_is_marked(curated):
    seconds, estimated, outdoor = transfer_seconds(curated, "A", "B")
    assert (seconds, estimated, outdoor) == (DEFAULT_TRANSFER_S, True, False)


def test_shipped_files_parse():
    # оба файла в репо должны читаться и содержать линии
    for city in ("msk", "spb"):
        c = load_curated(city)
        assert c.headways, f"{city}: интервалы не заданы"
        assert c.speeds, f"{city}: скорости фолбэка не заданы"
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_times.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'habitus.geo.metro_times'`

- [ ] **Step 3: Реализовать модуль**

Создать `habitus/geo/metro_times.py`:

```python
# habitus/geo/metro_times.py — курируемый слой времён поверх топологии из OSM.
#
# Ключ сопоставления — нормализованное имя станции плюс линия, НЕ osm_id:
# правка разметки в OSM не должна обнулять курированные данные.
import json
from dataclasses import dataclass, field
from pathlib import Path

from habitus.config import settings
from habitus.geo.metro import normalize_station_name

#: Пересадка, которой нет в курируемом файле. Три минуты — типовой подземный
#: переход; уличные переходы между системами существенно длиннее и обязаны
#: курироваться явно (вывести их из геометрии нельзя).
DEFAULT_TRANSFER_S = 180
#: Стоянка на станции, добавляемая к оценочному перегону.
STOP_DWELL_S = 25


@dataclass
class CuratedTimes:
    headways: dict[str, int] = field(default_factory=dict)
    speeds: dict[str, float] = field(default_factory=dict)
    edges: dict[tuple[str, str, str], int] = field(default_factory=dict)
    transfers: dict[tuple[str, str], int] = field(default_factory=dict)
    outdoor: set[tuple[str, str]] = field(default_factory=set)


def _pair(a: str, b: str) -> tuple[str, str]:
    """Ненаправленный ключ: пересадка одинакова в обе стороны."""
    x, y = normalize_station_name(a), normalize_station_name(b)
    return (x, y) if x <= y else (y, x)


def load_curated(city: str, data_dir: Path | None = None) -> CuratedTimes:
    path = (data_dir or settings.data_dir) / "metro" / f"{city}.json"
    raw = json.loads(path.read_text(encoding="utf-8"))
    c = CuratedTimes()
    for line in raw.get("lines", []):
        c.headways[line["ref"]] = int(line["headway_s"])
        c.speeds[line["ref"]] = float(line["fallback_speed_kmh"])
    for e in raw.get("edges", []):
        key = (e["line"], normalize_station_name(e["from"]),
               normalize_station_name(e["to"]))
        c.edges[key] = int(e["seconds"])
        # перегон ненаправленный: поезд идёт столько же в обратную сторону
        c.edges[(e["line"], key[2], key[1])] = int(e["seconds"])
    for t in raw.get("transfers", []):
        key = _pair(t["from"], t["to"])
        c.transfers[key] = int(t["seconds"])
        if t.get("outdoor"):
            c.outdoor.add(key)
    return c


def edge_seconds(curated: CuratedTimes, line_ref: str, a_name: str, b_name: str,
                 system: str, metres: float) -> tuple[int, bool]:
    """Секунды перегона и признак того, что это оценка, а не курированное значение."""
    key = (line_ref, normalize_station_name(a_name), normalize_station_name(b_name))
    if key in curated.edges:
        return curated.edges[key], False
    # Фолбэк: расстояние по геометрии линии на скорость ЭТОЙ линии. Одна
    # константа на все три системы врала бы систематически — у диаметров
    # перегонная скорость заметно выше метро.
    kmh = curated.speeds.get(line_ref) or _DEFAULT_SPEED_KMH[system]
    return int(round(metres / (kmh * 1000 / 3600))) + STOP_DWELL_S, True


#: Запасная скорость, когда линии нет даже в списке lines курируемого файла
#: (например, станция открылась и приехала из OSM раньше, чем её докурировали).
_DEFAULT_SPEED_KMH = {"subway": 40.0, "mck": 45.0, "mcd": 55.0}


def transfer_seconds(curated: CuratedTimes, a_name: str,
                     b_name: str) -> tuple[int, bool, bool]:
    """Секунды пересадки, признак оценки и признак уличного перехода."""
    key = _pair(a_name, b_name)
    if key in curated.transfers:
        return curated.transfers[key], False, key in curated.outdoor
    return DEFAULT_TRANSFER_S, True, False
```

- [ ] **Step 4: Завести курируемые файлы**

Создать `data/metro/msk.json`. Заполнить `lines` **для всех линий, которые вернула разведка в Задаче 1** (14+ линий метро, МЦК, D1–D5), `edges` и `transfers` — начиная с центральных пересадочных узлов. Формат:

```json
{
  "lines": [
    {"ref": "1",  "system": "subway", "headway_s": 120, "fallback_speed_kmh": 40},
    {"ref": "14", "system": "mck",    "headway_s": 300, "fallback_speed_kmh": 45},
    {"ref": "D1", "system": "mcd",    "headway_s": 600, "fallback_speed_kmh": 55}
  ],
  "edges": [
    {"line": "1", "from": "Сокольники", "to": "Красносельская", "seconds": 150}
  ],
  "transfers": [
    {"from": "Охотный Ряд", "to": "Театральная", "seconds": 180},
    {"from": "Площадь Гагарина", "to": "Ленинский проспект", "seconds": 420, "outdoor": true}
  ]
}
```

Создать `data/metro/spb.json` тем же форматом: пять линий метро, `system` везде `subway`, `edges` и `transfers` — по центральным узлам.

**Пустые `edges` — допустимое стартовое состояние:** каждый неописанный перегон получит оценку с пометкой `estimated`, граф не порвётся и не соврёт молча. Курирование можно наращивать инкрементально. Уличные пересадки между системами (МЦД↔метро) заполнить **обязательно** — они длиннее фолбэка вдвое-втрое.

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_metro_times.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/geo/metro_times.py data/metro/ tests/test_metro_times.py
git commit -m "feat: курируемые времена перегонов и пересадок с помеченным фолбэком"
```

---

## Task 5: Таблицы графа в схеме БД

**Files:**
- Modify: `habitus/db/schema.sql` (в конец файла, к остальным идемпотентным блокам)
- Test: `tests/test_metro_schema_db.py` (create)

**Interfaces:**
- Consumes: ничего.
- Produces: таблицы `metro_line`, `metro_station`, `metro_edge`, `metro_transfer`, `metro_line_geom`, `listing_metro_access` с колонками ровно того состава, который используют Задачи 6, 7, 9 и 12.

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_schema_db.py`:

```python
import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db

EXPECTED = {
    "metro_line": {"id", "city", "system", "ref", "name", "colour",
                   "headway_s", "fallback_speed_kmh", "updated_at"},
    "metro_station": {"id", "city", "line_id", "osm_id", "name", "name_norm",
                      "geom", "order_index", "updated_at"},
    "metro_edge": {"city", "from_station", "to_station", "seconds", "estimated"},
    "metro_transfer": {"city", "from_station", "to_station", "seconds",
                       "estimated", "outdoor"},
    "metro_line_geom": {"line_id", "geom"},
    "listing_metro_access": {"external_id", "station_id", "walk_seconds",
                             "estimated", "updated_at"},
}


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        yield c


def _columns(conn, table: str) -> set[str]:
    rows = conn.execute(
        "SELECT column_name FROM information_schema.columns WHERE table_name = %s",
        (table,)).fetchall()
    return {r[0] for r in rows}


@pytest.mark.parametrize("table,cols", EXPECTED.items())
def test_table_has_expected_columns(conn, table, cols):
    assert cols <= _columns(conn, table), f"{table}: не хватает колонок"


def test_init_db_is_idempotent(conn):
    # схема применяется поверх себя без ошибок — иначе повторный offline упадёт
    init_db(conn)
    assert _columns(conn, "metro_line")


def test_station_geom_is_indexed(conn):
    rows = conn.execute(
        "SELECT indexdef FROM pg_indexes WHERE tablename = 'metro_station'"
    ).fetchall()
    assert any("gist" in r[0].lower() for r in rows), "нет GIST по geom"
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_schema_db.py -v`
Expected: FAIL — таблиц нет (либо SKIP, если Postgres не поднят; тогда поднять его: `docker compose up -d db`)

- [ ] **Step 3: Реализовать**

Дописать в конец `habitus/db/schema.sql`:

```sql
-- Рельсовый транспорт: метро, МЦК, МЦД. Префикс metro_ сохраняется для всех
-- трёх систем сознательно: публичный контракт уже называет эту сущность
-- «metro» в трёх местах (TravelMode, GeoConstraint.kind, enum слоёв карты), и
-- переименование ради формальной точности порвало бы зафиксированные на трёх
-- сторонах enum'ы без выигрыша для пользователя. Систему различает колонка.
--
-- Узел графа — ПЛАТФОРМА ОДНОЙ ЛИНИИ, а не станция как здание: «Охотный Ряд»
-- и «Театральная» — два узла, связанные строкой в metro_transfer.
CREATE TABLE IF NOT EXISTS metro_line (
    id                 BIGSERIAL PRIMARY KEY,
    city               TEXT NOT NULL,
    system             TEXT NOT NULL CHECK (system IN ('subway','mck','mcd')),
    ref                TEXT NOT NULL,
    name               TEXT NOT NULL,
    colour             TEXT,
    -- Интервал и скорость фолбэка — ДАННЫЕ, а не логика: у метро интервал
    -- около двух минут, у МЦК пять-восемь, у диаметров днём до двенадцати;
    -- перегонная скорость у диаметров заметно выше метро.
    headway_s          INTEGER NOT NULL,
    fallback_speed_kmh REAL NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (city, system, ref)
);

CREATE TABLE IF NOT EXISTS metro_station (
    id          BIGSERIAL PRIMARY KEY,
    city        TEXT NOT NULL,
    line_id     BIGINT NOT NULL REFERENCES metro_line(id) ON DELETE CASCADE,
    osm_id      BIGINT,
    name        TEXT NOT NULL,
    name_norm   TEXT NOT NULL,
    geom        geometry(Point, 4326) NOT NULL,
    order_index INTEGER NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (line_id, order_index)
);

CREATE TABLE IF NOT EXISTS metro_edge (
    city         TEXT NOT NULL,
    from_station BIGINT NOT NULL REFERENCES metro_station(id) ON DELETE CASCADE,
    to_station   BIGINT NOT NULL REFERENCES metro_station(id) ON DELETE CASCADE,
    seconds      INTEGER NOT NULL,
    -- true — время выведено из расстояния, а не взято из курируемого файла.
    -- Признак едет наружу до фронта: оценка показывается как оценка.
    estimated    BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (from_station, to_station)
);

CREATE TABLE IF NOT EXISTS metro_transfer (
    city         TEXT NOT NULL,
    from_station BIGINT NOT NULL REFERENCES metro_station(id) ON DELETE CASCADE,
    to_station   BIGINT NOT NULL REFERENCES metro_station(id) ON DELETE CASCADE,
    seconds      INTEGER NOT NULL,
    estimated    BOOLEAN NOT NULL DEFAULT FALSE,
    -- Переход улицей (типично между метро и МЦД): 5–10 минут вместо трёх.
    outdoor      BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (from_station, to_station)
);

CREATE TABLE IF NOT EXISTS metro_line_geom (
    line_id BIGINT PRIMARY KEY REFERENCES metro_line(id) ON DELETE CASCADE,
    geom    geometry(LineString, 4326)
);

-- Пешие плечи «объект → платформа». Несколько строк на объект: у дома возле
-- пересадочного узла в пешей доступности несколько платформ, и движку нужны
-- все — ближайшая по прямой регулярно оказывается на тупиковой ветке.
CREATE TABLE IF NOT EXISTS listing_metro_access (
    external_id  TEXT NOT NULL,
    station_id   BIGINT NOT NULL REFERENCES metro_station(id) ON DELETE CASCADE,
    walk_seconds INTEGER NOT NULL,
    estimated    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (external_id, station_id)
);

CREATE INDEX IF NOT EXISTS metro_line_city_system_ix ON metro_line (city, system);
CREATE INDEX IF NOT EXISTS metro_station_geom_gix ON metro_station USING GIST (geom);
CREATE INDEX IF NOT EXISTS metro_station_city_ix ON metro_station (city);
CREATE INDEX IF NOT EXISTS metro_station_norm_ix ON metro_station (city, name_norm);
CREATE INDEX IF NOT EXISTS metro_edge_city_ix ON metro_edge (city);
CREATE INDEX IF NOT EXISTS metro_transfer_city_ix ON metro_transfer (city);
CREATE INDEX IF NOT EXISTS lma_station_ix ON listing_metro_access (station_id);
```

- [ ] **Step 4: Запустить тесты**

Run: `uv run pytest tests/test_metro_schema_db.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/db/schema.sql tests/test_metro_schema_db.py
git commit -m "feat: таблицы графа метро, МЦК и МЦД"
```

---

## Task 6: Сборка графа в БД

**Files:**
- Modify: `habitus/geo/metro.py` (добавить `upsert_transit`)
- Test: `tests/test_metro_build_db.py` (create)

**Interfaces:**
- Consumes: `LineRaw`, `StationRaw`, `normalize_station_name` (Задача 3); `load_curated`, `edge_seconds`, `transfer_seconds` (Задача 4); таблицы (Задача 5).
- Produces: `upsert_transit(lines: list[LineRaw], conn, city: str, curated: CuratedTimes | None = None) -> dict[str, int]` — возвращает статистику `{"lines": n, "stations": n, "edges": n, "transfers": n}`.

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_build_db.py`:

```python
import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro import LineRaw, StationRaw, upsert_transit
from habitus.geo.metro_times import CuratedTimes


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        c.commit()
        yield c


def _line(ref="1", system="subway", names=("A", "B", "C"), lon0=37.60):
    return LineRaw(
        system=system, ref=ref, name=f"линия {ref}", colour="#EF161E",
        stations=[StationRaw(osm_id=1000 + i, name=n, lon=lon0 + i * 0.02,
                             lat=55.75)
                  for i, n in enumerate(names)],
        geometry=[[lon0 + i * 0.02, 55.75] for i in range(len(names))])


def _curated():
    c = CuratedTimes()
    c.headways = {"1": 120, "2": 120}
    c.speeds = {"1": 40.0, "2": 40.0}
    c.edges = {("1", "a", "b"): 150, ("1", "b", "a"): 150}
    return c


def test_stations_keep_order_and_normalized_name(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    rows = conn.execute(
        "SELECT name, name_norm, order_index FROM metro_station ORDER BY order_index"
    ).fetchall()
    assert [r[0] for r in rows] == ["A", "B", "C"]
    assert [r[1] for r in rows] == ["a", "b", "c"]
    assert [r[2] for r in rows] == [0, 1, 2]


def test_edges_link_consecutive_stations_both_ways(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    n = conn.execute("SELECT count(*) FROM metro_edge").fetchone()[0]
    assert n == 4   # A↔B и B↔C, по два направления


def test_curated_edge_wins_and_missing_one_is_marked(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    rows = dict(conn.execute("""
        SELECT s1.name || '-' || s2.name, e.estimated
        FROM metro_edge e
        JOIN metro_station s1 ON s1.id = e.from_station
        JOIN metro_station s2 ON s2.id = e.to_station""").fetchall())
    assert rows["A-B"] is False        # есть в курируемом файле
    assert rows["B-C"] is True         # нет — оценка, помечена


def test_transfer_created_between_same_named_stations_on_other_lines(conn):
    upsert_transit([_line("1", names=("A", "B", "C")),
                    _line("2", names=("B", "X", "Y"), lon0=37.62)],
                   conn, "msk", _curated())
    rows = conn.execute("""
        SELECT s1.name, s2.name FROM metro_transfer t
        JOIN metro_station s1 ON s1.id = t.from_station
        JOIN metro_station s2 ON s2.id = t.to_station""").fetchall()
    # одноимённая станция на двух линиях — это пересадочный узел
    assert {(a, b) for a, b in rows} == {("B", "B")}


def test_line_geometry_stored(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    row = conn.execute(
        "SELECT ST_GeometryType(geom) FROM metro_line_geom").fetchone()
    assert row[0] == "ST_LineString"


def test_rerun_does_not_duplicate(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    before = conn.execute("SELECT count(*) FROM metro_station").fetchone()[0]
    upsert_transit([_line()], conn, "msk", _curated())
    after = conn.execute("SELECT count(*) FROM metro_station").fetchone()[0]
    assert before == after == 3


def test_headway_comes_from_curated_data(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    assert conn.execute(
        "SELECT headway_s FROM metro_line WHERE ref='1'").fetchone()[0] == 120
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_build_db.py -v`
Expected: FAIL — `ImportError: cannot import name 'upsert_transit'`

- [ ] **Step 3: Реализовать**

Дописать в `habitus/geo/metro.py`:

```python
import psycopg

from habitus.geo.metro_times import (CuratedTimes, edge_seconds, load_curated,
                                     transfer_seconds)

#: Скорость и интервал линии, которой нет в курируемом файле вовсе (станция
#: открылась и приехала из OSM раньше, чем её докурировали). Без них строку в
#: metro_line не вставить: колонки NOT NULL.
_UNCURATED_HEADWAY_S = {"subway": 150, "mck": 360, "mcd": 720}
_UNCURATED_SPEED_KMH = {"subway": 40.0, "mck": 45.0, "mcd": 55.0}


def upsert_transit(lines: list[LineRaw], conn: psycopg.Connection, city: str,
                   curated: CuratedTimes | None = None) -> dict[str, int]:
    """Линии из OSM + курируемые времена → граф в БД. Идемпотентно."""
    cur_times = curated if curated is not None else load_curated(city)
    stats = {"lines": 0, "stations": 0, "edges": 0, "transfers": 0}

    with conn.cursor() as cur:
        for line in lines:
            cur.execute("""
                INSERT INTO metro_line (city, system, ref, name, colour,
                                        headway_s, fallback_speed_kmh)
                VALUES (%s,%s,%s,%s,%s,%s,%s)
                ON CONFLICT (city, system, ref) DO UPDATE SET
                    name = EXCLUDED.name, colour = EXCLUDED.colour,
                    headway_s = EXCLUDED.headway_s,
                    fallback_speed_kmh = EXCLUDED.fallback_speed_kmh,
                    updated_at = now()
                RETURNING id;""",
                (city, line.system, line.ref, line.name, line.colour,
                 cur_times.headways.get(line.ref,
                                        _UNCURATED_HEADWAY_S[line.system]),
                 cur_times.speeds.get(line.ref,
                                      _UNCURATED_SPEED_KMH[line.system])))
            line_id = cur.fetchone()[0]
            stats["lines"] += 1

            # Перестраиваем станции линии целиком: порядок следования мог
            # измениться (продлили ветку в начало), а order_index — часть
            # уникального ключа. Каскад снимет старые рёбра этой линии.
            cur.execute("DELETE FROM metro_station WHERE line_id = %s;", (line_id,))
            ids: list[int] = []
            for i, st in enumerate(line.stations):
                cur.execute("""
                    INSERT INTO metro_station (city, line_id, osm_id, name,
                                               name_norm, geom, order_index)
                    VALUES (%s,%s,%s,%s,%s,
                            ST_SetSRID(ST_MakePoint(%s,%s),4326),%s)
                    RETURNING id;""",
                    (city, line_id, st.osm_id, st.name,
                     normalize_station_name(st.name), st.lon, st.lat, i))
                ids.append(cur.fetchone()[0])
                stats["stations"] += 1

            if len(line.geometry) >= 2:
                cur.execute("""
                    INSERT INTO metro_line_geom (line_id, geom)
                    VALUES (%s, ST_SetSRID(ST_GeomFromGeoJSON(%s),4326))
                    ON CONFLICT (line_id) DO UPDATE SET geom = EXCLUDED.geom;""",
                    (line_id, json.dumps({"type": "LineString",
                                          "coordinates": line.geometry})))

            for i in range(len(ids) - 1):
                a, b = line.stations[i], line.stations[i + 1]
                cur.execute("""SELECT ST_Distance(
                                  (SELECT geom FROM metro_station WHERE id=%s)::geography,
                                  (SELECT geom FROM metro_station WHERE id=%s)::geography);""",
                            (ids[i], ids[i + 1]))
                metres = float(cur.fetchone()[0])
                seconds, estimated = edge_seconds(cur_times, line.ref, a.name,
                                                  b.name, line.system, metres)
                for x, y in ((ids[i], ids[i + 1]), (ids[i + 1], ids[i])):
                    cur.execute("""
                        INSERT INTO metro_edge (city, from_station, to_station,
                                                seconds, estimated)
                        VALUES (%s,%s,%s,%s,%s)
                        ON CONFLICT (from_station, to_station) DO UPDATE SET
                            seconds = EXCLUDED.seconds,
                            estimated = EXCLUDED.estimated;""",
                        (city, x, y, seconds, estimated))
                    stats["edges"] += 1

        # Пересадка — одноимённые платформы на разных линиях. Имя сверяется
        # нормализованным: в OSM одна и та же станция пишется по-разному.
        cur.execute("""
            SELECT a.id, b.id, a.name, b.name
            FROM metro_station a JOIN metro_station b
              ON a.name_norm = b.name_norm AND a.line_id <> b.line_id
            WHERE a.city = %s AND b.city = %s;""", (city, city))
        for a_id, b_id, a_name, b_name in cur.fetchall():
            seconds, estimated, outdoor = transfer_seconds(cur_times, a_name, b_name)
            cur.execute("""
                INSERT INTO metro_transfer (city, from_station, to_station,
                                            seconds, estimated, outdoor)
                VALUES (%s,%s,%s,%s,%s,%s)
                ON CONFLICT (from_station, to_station) DO UPDATE SET
                    seconds = EXCLUDED.seconds, estimated = EXCLUDED.estimated,
                    outdoor = EXCLUDED.outdoor;""",
                (city, a_id, b_id, seconds, estimated, outdoor))
            stats["transfers"] += 1

    conn.commit()
    return stats
```

Добавить `import json` в начало `habitus/geo/metro.py`, если его там ещё нет.

- [ ] **Step 4: Запустить тесты**

Run: `uv run pytest tests/test_metro_build_db.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/geo/metro.py tests/test_metro_build_db.py
git commit -m "feat: сборка графа метро в БД из OSM и курируемых времён"
```

---

## Task 7: Пешие плечи «объект → станция»

**Files:**
- Create: `habitus/geo/metro_access.py`
- Create: `tests/test_metro_access_db.py`
- Modify: `habitus/geo/enrich.py:80-82` (источник `walk_min_metro`)

**Interfaces:**
- Consumes: таблицы (Задача 5), станции в БД (Задача 6).
- Produces:
  - `WALK_DETOUR = 1.3`, `WALK_SPEED_MPS = 1.33`
  - `straight_walk_seconds(metres: float) -> int`
  - `refresh_listing_metro_access(conn, city: str, walker=None, k: int = 3) -> int` — `walker` это `Callable[[tuple[float,float], tuple[float,float]], float | None]`, возвращающая секунды по пешей сети или `None` при отказе.
  - `refresh_walk_min_metro(conn, city: str) -> int`
  - `ORSWalker` — обёртка над `ORSProvider.directions` с профилем `foot-walking`.

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_access_db.py`:

```python
import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro_access import (WALK_DETOUR, refresh_listing_metro_access,
                                      refresh_walk_min_metro,
                                      straight_walk_seconds)


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings CASCADE;")
            cur.execute("TRUNCATE metro_line CASCADE;")
            cur.execute("""INSERT INTO metro_line
                           (id, city, system, ref, name, headway_s, fallback_speed_kmh)
                           VALUES (1,'msk','subway','1','л1',120,40),
                                  (2,'msk','mcd','D1','д1',600,55);""")
            # три платформы: две подземных рядом, одна МЦД чуть дальше
            cur.execute("""INSERT INTO metro_station
                           (id, city, line_id, name, name_norm, geom, order_index)
                           VALUES
                           (10,'msk',1,'Ближняя','ближняя',
                            ST_SetSRID(ST_MakePoint(37.6005,55.75),4326),0),
                           (11,'msk',1,'Дальняя','дальняя',
                            ST_SetSRID(ST_MakePoint(37.610,55.75),4326),1),
                           (12,'msk',2,'Диаметр','диаметр',
                            ST_SetSRID(ST_MakePoint(37.601,55.75),4326),0);""")
            cur.execute("""INSERT INTO listings (external_id, source, is_active,
                               city, geom)
                           VALUES ('A','test',TRUE,'msk',
                                   ST_SetSRID(ST_MakePoint(37.60,55.75),4326));""")
        c.commit()
        yield c


def test_straight_walk_applies_detour_factor():
    # прямая по воздуху занижает: между домом и станцией бывает река или пути
    assert straight_walk_seconds(1000) == int(round(1000 * WALK_DETOUR / 1.33))


def test_without_walker_falls_back_and_marks_estimated(conn):
    n = refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    assert n == 3
    rows = conn.execute(
        "SELECT station_id, estimated FROM listing_metro_access ORDER BY station_id"
    ).fetchall()
    assert [r[0] for r in rows] == [10, 11, 12]
    assert all(r[1] is True for r in rows)


def test_keeps_only_k_nearest(conn):
    refresh_listing_metro_access(conn, "msk", walker=None, k=2)
    ids = [r[0] for r in conn.execute(
        "SELECT station_id FROM listing_metro_access ORDER BY walk_seconds").fetchall()]
    assert len(ids) == 2 and 10 in ids


def test_network_walker_wins_over_straight_line(conn):
    # сеть возвращает 600 с там, где прямая дала бы меньше — значение из сети
    refresh_listing_metro_access(conn, "msk", walker=lambda a, b: 600.0, k=1)
    row = conn.execute(
        "SELECT walk_seconds, estimated FROM listing_metro_access").fetchone()
    assert row == (600, False)


def test_walker_failure_degrades_per_station_not_globally(conn):
    def flaky(a, b):
        raise RuntimeError("ORS упал")

    n = refresh_listing_metro_access(conn, "msk", walker=flaky, k=2)
    assert n == 2   # строки всё равно есть
    assert all(r[0] is True for r in
               conn.execute("SELECT estimated FROM listing_metro_access").fetchall())


def test_walk_min_metro_counts_subway_only(conn):
    # МЦД-платформа ближе подземной «Дальней», но в walk_min_metro попадать
    # не должна: поле и фильтр geo kind=metro остаются про подземку
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    conn.execute("UPDATE listing_metro_access SET walk_seconds = 60 WHERE station_id = 12;")
    conn.commit()
    refresh_walk_min_metro(conn, "msk")
    got = conn.execute(
        "SELECT walk_min_metro FROM listings WHERE external_id='A'").fetchone()[0]
    assert got > 1.5, "минуты взяты с платформы МЦД — так быть не должно"


def test_rerun_is_idempotent(conn):
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    n = conn.execute("SELECT count(*) FROM listing_metro_access").fetchone()[0]
    assert n == 3
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_access_db.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'habitus.geo.metro_access'`

- [ ] **Step 3: Реализовать**

Создать `habitus/geo/metro_access.py`:

```python
# habitus/geo/metro_access.py — пешие плечи «объект → платформа».
import logging

import psycopg
import requests

from habitus.online.geo import ORSProvider

log = logging.getLogger(__name__)

WALK_SPEED_MPS = 1.33          # средняя пешая скорость
#: Коэффициент извилистости: прямая по воздуху систематически занижает время —
#: между домом и станцией бывает река, выемка путей или закрытый квартал.
WALK_DETOUR = 1.3


def straight_walk_seconds(metres: float) -> int:
    return int(round(metres * WALK_DETOUR / WALK_SPEED_MPS))


class ORSWalker:
    """Пешая сеть через ORS. Вызывается как walker(start, end) → секунды."""

    def __init__(self, provider: ORSProvider | None = None):
        self._provider = provider or ORSProvider()

    def __call__(self, start: tuple[float, float],
                 end: tuple[float, float]) -> float | None:
        _, seconds = self._provider.directions(start, end, "foot-walking")
        return seconds


def refresh_listing_metro_access(conn: psycopg.Connection, city: str,
                                 walker=None, k: int = 3) -> int:
    """Три ближайшие платформы на объект с пешим временем до каждой.

    Три, а не одна: ближайшая по прямой платформа регулярно оказывается на
    тупиковой ветке, тогда как вторая по близости стоит на пересадочном узле и
    даёт маршрут заметно короче. Выбор входа делает уже движок.

    Кандидаты добираются KNN-оператором <-> с запасом: он упорядочивает по
    ПЛАНАРНОМУ расстоянию в градусах, а планарно ближайшая точка на широте
    Москвы не всегда геодезически ближайшая (тот же приём и та же причина, что
    в habitus/geo/enrich.py).
    """
    written = 0
    with conn.cursor() as cur:
        cur.execute("""
            SELECT l.external_id, ST_X(l.geom), ST_Y(l.geom)
            FROM listings l
            WHERE l.city = %s AND l.geom IS NOT NULL;""", (city,))
        listings = cur.fetchall()

        for ext_id, lon, lat in listings:
            cur.execute("""
                SELECT s.id, ST_X(s.geom), ST_Y(s.geom),
                       ST_Distance(s.geom::geography,
                                   ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography)
                FROM (SELECT id, geom FROM metro_station
                      WHERE city = %s
                      ORDER BY geom <-> ST_SetSRID(ST_MakePoint(%s,%s),4326)
                      LIMIT %s) s
                ORDER BY 4
                LIMIT %s;""",
                (lon, lat, city, lon, lat, max(k * 3, 9), k))
            cur.execute("DELETE FROM listing_metro_access WHERE external_id = %s;",
                        (ext_id,))
            for st_id, s_lon, s_lat, metres in cur.fetchall() or []:
                seconds, estimated = straight_walk_seconds(metres), True
                if walker is not None:
                    try:
                        got = walker((lon, lat), (s_lon, s_lat))
                        if got is not None:
                            seconds, estimated = int(round(got)), False
                    except (requests.RequestException, KeyError, TypeError,
                            ValueError, RuntimeError) as exc:
                        # Отказ пешего роутера деградирует ОДНУ станцию до
                        # оценки, а не роняет весь прогон: тем же принципом,
                        # которым защищён сбор POI в habitus/cli.py.
                        log.warning("пеший роутер отказал на %s→%s: %s",
                                    ext_id, st_id, exc)
                cur.execute("""
                    INSERT INTO listing_metro_access
                        (external_id, station_id, walk_seconds, estimated)
                    VALUES (%s,%s,%s,%s)
                    ON CONFLICT (external_id, station_id) DO UPDATE SET
                        walk_seconds = EXCLUDED.walk_seconds,
                        estimated = EXCLUDED.estimated, updated_at = now();""",
                    (ext_id, st_id, seconds, estimated))
                written += 1
    conn.commit()
    return written


def refresh_walk_min_metro(conn: psycopg.Connection, city: str) -> int:
    """walk_min_metro из посчитанных плеч — вместо прямой по воздуху.

    ТОЛЬКО подземка: платформы МЦК и МЦД в это поле не подмешиваются. Поле
    участвует в proximity-ранжировании, а пороги гейта `eval --check` измерены
    на текущих данных (docs/notes/eval-baseline-2026-08-18.md) — тихая подмена
    смысла поля сдвинула бы выдачу и обесценила baseline.
    """
    with conn.cursor() as cur:
        cur.execute("""
            UPDATE listings l SET
                walk_min_metro = COALESCE(l.walk_min_metro_src, sub.minutes),
                metro_station = COALESCE(l.metro_station, sub.name),
                updated_at = now()
            FROM (
                SELECT DISTINCT ON (a.external_id)
                       a.external_id, a.walk_seconds / 60.0 AS minutes, s.name
                FROM listing_metro_access a
                JOIN metro_station s ON s.id = a.station_id
                JOIN metro_line ml ON ml.id = s.line_id
                WHERE ml.system = 'subway' AND s.city = %s
                ORDER BY a.external_id, a.walk_seconds
            ) sub
            WHERE l.external_id = sub.external_id AND l.city = %s;""",
            (city, city))
        n = cur.rowcount
    conn.commit()
    return n
```

- [ ] **Step 4: Убрать вычисление метро по прямой из enrich.py**

В `habitus/geo/enrich.py` заменить строку 80 так, чтобы `enrich_all` больше не пересчитывал `walk_min_metro` по прямой и не затирал значение, посчитанное `refresh_walk_min_metro`:

```python
  -- walk_min_metro здесь НЕ считается: его владелец — habitus/geo/metro_access.py,
  -- который берёт время по пешей сети до платформы подземки. Прямая по воздуху
  -- (прежний _nearest_min('metro')) занижала на реке, путях и закрытых кварталах.
  -- Порядок в offline-прогоне: enrich_all → refresh_listing_metro_access →
  -- refresh_walk_min_metro, поэтому здесь колонка не трогается вовсе.
```

Строку `walk_min_metro  = COALESCE(l.walk_min_metro_src, {_nearest_min('metro')}),` и строку `metro_station   = COALESCE(l.metro_station, {_nearest_name('metro')}),` удалить из `_ENRICH_SQL`.

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_metro_access_db.py tests/test_enrich.py -v`
Expected: PASS. Если `tests/test_enrich.py` проверяет `walk_min_metro` — поправить его на новый источник, а не возвращать расчёт по прямой.

- [ ] **Step 6: Commit**

```bash
git add habitus/geo/metro_access.py habitus/geo/enrich.py tests/test_metro_access_db.py tests/test_enrich.py
git commit -m "feat: пешие плечи до платформ по сети вместо прямой по воздуху"
```

---

## Task 8: CLI-команда сборки метро

**Files:**
- Modify: `habitus/cli.py`
- Test: `tests/test_cli_smoke.py`

**Interfaces:**
- Consumes: `fetch_system`, `upsert_transit` (Задачи 3, 6); `refresh_listing_metro_access`, `refresh_walk_min_metro`, `ORSWalker` (Задача 7).
- Produces: `build_metro(conn, city: str, fetch=fetch_system, walker=None) -> dict` и подкоманда `habitus metro --city msk`.

- [ ] **Step 1: Написать падающий тест**

Добавить в `tests/test_cli_smoke.py`:

```python
from habitus.cli import build_metro


def test_build_metro_reports_failed_systems_without_dying(monkeypatch):
    calls = []

    def fake_fetch(system, city):
        calls.append((system, city))
        if system == "mcd":
            raise RuntimeError("Overpass 504")
        return []

    class FakeConn:
        def rollback(self): pass
        def commit(self): pass

    stats = build_metro(FakeConn(), "msk", fetch=fake_fetch)
    assert [c[0] for c in calls] == ["subway", "mck", "mcd"]
    # отказ одной системы не уносит остальные и не глотается молча
    assert any("mcd" in f for f in stats["failed"])
    assert "subway" not in " ".join(stats["failed"])
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_cli_smoke.py -v`
Expected: FAIL — `ImportError: cannot import name 'build_metro'`

- [ ] **Step 3: Реализовать**

В `habitus/cli.py` добавить импорты и функцию:

```python
from habitus.geo.metro import SYSTEMS, fetch_system, upsert_transit
from habitus.geo.metro_access import (ORSWalker, refresh_listing_metro_access,
                                      refresh_walk_min_metro)


def build_metro(conn, city: str, fetch=fetch_system, walker=None) -> dict:
    """Граф рельсового транспорта города: OSM → БД → пешие плечи объектов.

    Отказ одной системы не уносит остальные и не глотается молча — тем же
    принципом, которым защищён сбор POI: Overpass регулярно отдаёт 504.
    """
    stats: dict = {"failed": []}
    for system in SYSTEMS:
        try:
            lines = fetch(system, city)
            stats[system] = upsert_transit(lines, conn, city)
        except Exception as e:  # noqa: BLE001 — внешний API, причин отказа много
            conn.rollback()
            stats["failed"].append(f"{system}/{city}: {e}")
    stats["access"] = refresh_listing_metro_access(conn, city, walker=walker)
    stats["walk_min_metro"] = refresh_walk_min_metro(conn, city)
    return stats
```

В `main()` добавить парсер и ветку:

```python
    metro = sub.add_parser("metro")
    metro.add_argument("--city", choices=["msk", "spb"], default="msk")
    metro.add_argument("--no-ors", action="store_true",
                       help="не ходить в ORS: пешие плечи оценкой по прямой")
```

```python
        elif args.cmd == "metro":
            walker = None if args.no_ors or not settings.ors_api_key else ORSWalker()
            print(build_metro(conn, args.city, walker=walker))
```

Вызвать `build_metro` из `run_offline` после `enrich_all`, чтобы порядок был `enrich_all → build_metro`:

```python
    stats["enriched"] = enrich_all(conn)
    if fetch_osm:
        stats["metro"] = build_metro(
            conn, city,
            walker=ORSWalker() if settings.ors_api_key else None)
```

- [ ] **Step 4: Запустить тесты**

Run: `uv run pytest tests/test_cli_smoke.py -v`
Expected: PASS

- [ ] **Step 5: Обновить README**

В `README.md` в раздел про оффлайн-пайплайн добавить:

```markdown
Граф рельсового транспорта (метро, МЦК, МЦД) собирается отдельной командой —
`uv run habitus metro --city msk` и `uv run habitus metro --city spb`. Она же
пересчитывает пешие плечи объектов до платформ. Без ключа ORS
(`ORS_API_KEY`) плечи считаются оценкой по прямой и помечаются `estimated`.
```

- [ ] **Step 6: Commit**

```bash
git add habitus/cli.py tests/test_cli_smoke.py README.md
git commit -m "feat: команда habitus metro для сборки графа рельсового транспорта"
```

---

# Этап B — движок и контракт

## Task 9: Дейкстра по графу

**Files:**
- Create: `habitus/online/metro_route.py`
- Create: `tests/test_metro_route.py`

**Interfaces:**
- Consumes: таблицы графа (Задачи 5, 6).
- Produces:
  - `@dataclass Station(id: int, name: str, line_ref: str, line_name: str, system: str, colour: str | None, lon: float, lat: float)`
  - `@dataclass Segment(line_ref, line_name, system, colour, from_station: str, to_station: str, stops: int, seconds: int, estimated: bool)`
  - `@dataclass Transfer(from_station: str, to_station: str, seconds: int, estimated: bool, outdoor: bool)`
  - `@dataclass MetroRoute(segments: list[Segment], transfers: list[Transfer], ride_seconds: int, estimated: bool)`
  - `class MetroGraph` с методами `route(seeds: dict[int, int], targets: dict[int, int]) -> MetroRoute | None` и `times_from(seeds: dict[int, int]) -> dict[int, int]`
  - `load_graph(conn, city: str) -> MetroGraph` (кэш на процесс)
  - `clear_graph_cache() -> None`

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_route.py`:

```python
import pytest

from habitus.online.metro_route import MetroGraph, Station

#  линия 1:  A(1) — B(2) — C(3)      по 100 с
#  линия 2:  B'(4) — D(5)            120 с
#  пересадка B(2) ↔ B'(4): 180 с, headway линии 2 = 60 с
STATIONS = {
    1: Station(1, "A", "1", "линия 1", "subway", "#f00", 37.60, 55.75),
    2: Station(2, "B", "1", "линия 1", "subway", "#f00", 37.62, 55.75),
    3: Station(3, "C", "1", "линия 1", "subway", "#f00", 37.64, 55.75),
    4: Station(4, "B", "2", "линия 2", "mcd", "#00f", 37.62, 55.75),
    5: Station(5, "D", "2", "линия 2", "mcd", "#00f", 37.62, 55.78),
}
EDGES = {
    (1, 2): (100, False), (2, 1): (100, False),
    (2, 3): (100, True),  (3, 2): (100, True),
    (4, 5): (120, False), (5, 4): (120, False),
}
TRANSFERS = {(2, 4): (180, False, True), (4, 2): (180, False, True)}
HEADWAYS = {"1": 120, "2": 600}


@pytest.fixture
def graph() -> MetroGraph:
    return MetroGraph(stations=STATIONS, edges=EDGES, transfers=TRANSFERS,
                      headways=HEADWAYS)


def test_direct_ride_on_one_line(graph):
    r = graph.route({1: 0}, {3: 0})
    assert len(r.segments) == 1 and not r.transfers
    seg = r.segments[0]
    assert (seg.from_station, seg.to_station, seg.stops) == ("A", "C", 2)
    # 200 с езды + интервал линии 1 на посадку
    assert r.ride_seconds == 200 + HEADWAYS["1"]


def test_estimated_edge_taints_the_whole_route(graph):
    # честность важнее оптимизма: маршрут с оценочным перегоном — оценочный
    assert graph.route({1: 0}, {3: 0}).estimated is True
    assert graph.route({1: 0}, {2: 0}).estimated is False


def test_transfer_costs_walk_plus_new_line_headway(graph):
    r = graph.route({1: 0}, {5: 0})
    assert len(r.segments) == 2 and len(r.transfers) == 1
    assert r.transfers[0].outdoor is True
    # 100 (A→B) + 120 (посадка на 1) + 180 (переход) + 600 (интервал 2) + 120 (B'→D)
    assert r.ride_seconds == 100 + 120 + 180 + 600 + 120


def test_segments_carry_line_identity_for_rendering(graph):
    r = graph.route({1: 0}, {5: 0})
    assert [s.system for s in r.segments] == ["subway", "mcd"]
    assert [s.line_ref for s in r.segments] == ["1", "2"]
    assert r.segments[1].colour == "#00f"


def test_seed_walk_seconds_choose_the_better_entrance(graph):
    # вход через A дороже пешком, но через C ехать некуда — берётся A
    assert graph.route({1: 60, 3: 900}, {2: 0}).segments[0].from_station == "A"


def test_target_walk_seconds_are_included_in_choice(graph):
    fast = graph.route({1: 0}, {2: 0}).ride_seconds
    slow = graph.route({1: 0}, {2: 300}).ride_seconds
    assert slow == fast + 300


def test_times_from_is_one_pass_to_all(graph):
    times = graph.times_from({1: 0})
    assert times[1] == 0
    assert times[2] == 100 + HEADWAYS["1"]
    assert 5 in times, "через пересадку станция должна быть достижима"


def test_times_from_honours_multiple_seeds(graph):
    # два входа с разными пешими плечами: минимум берётся сам, без сравнений снаружи
    times = graph.times_from({1: 0, 3: 10})
    assert times[2] == min(100 + HEADWAYS["1"], 10 + 100 + HEADWAYS["1"])


def test_unreachable_returns_none(graph):
    lonely = MetroGraph(stations={9: STATIONS[1]}, edges={}, transfers={},
                        headways=HEADWAYS)
    assert lonely.route({9: 0}, {1: 0}) is None


def test_empty_seeds_or_targets_return_none(graph):
    assert graph.route({}, {3: 0}) is None
    assert graph.route({1: 0}, {}) is None
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_route.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'habitus.online.metro_route'`

- [ ] **Step 3: Реализовать**

Создать `habitus/online/metro_route.py`:

```python
# habitus/online/metro_route.py — граф рельсового транспорта в памяти и Дейкстра.
#
# Граф крошечный (порядка 300 узлов в Москве, 70 в Петербурге), поэтому обход
# считается за доли миллисекунды и предрасчитанная матрица не нужна: и число
# для SQL-фильтра, и разбивка маршрута для отрисовки берутся из ОДНОГО обхода
# одного графа — разойтись им нечему.
import heapq
from dataclasses import dataclass, field

import psycopg


@dataclass(frozen=True)
class Station:
    id: int
    name: str
    line_ref: str
    line_name: str
    system: str
    colour: str | None
    lon: float
    lat: float


@dataclass
class Segment:
    line_ref: str
    line_name: str
    system: str
    colour: str | None
    from_station: str
    to_station: str
    stops: int
    seconds: int
    estimated: bool = False


@dataclass
class Transfer:
    from_station: str
    to_station: str
    seconds: int
    estimated: bool = False
    outdoor: bool = False


@dataclass
class MetroRoute:
    segments: list[Segment] = field(default_factory=list)
    transfers: list[Transfer] = field(default_factory=list)
    ride_seconds: int = 0
    estimated: bool = False


@dataclass
class MetroGraph:
    stations: dict[int, Station]
    #: (from_id, to_id) → (секунды, оценка ли)
    edges: dict[tuple[int, int], tuple[int, bool]]
    #: (from_id, to_id) → (секунды, оценка ли, улицей ли)
    transfers: dict[tuple[int, int], tuple[int, bool, bool]]
    #: ref линии → интервал в секундах
    headways: dict[str, int]

    def _adjacency(self) -> dict[int, list[tuple[int, int, str]]]:
        """id → [(сосед, секунды, вид перехода)]. Строится лениво один раз."""
        if getattr(self, "_adj", None) is None:
            adj: dict[int, list[tuple[int, int, str]]] = {}
            for (a, b), (sec, _) in self.edges.items():
                adj.setdefault(a, []).append((b, sec, "ride"))
            for (a, b), (sec, _, _) in self.transfers.items():
                # Пересадка стоит перехода ПЛЮС интервал линии, на которую
                # садимся: ждать поезд придётся в любом случае.
                line = self.stations[b].line_ref
                adj.setdefault(a, []).append(
                    (b, sec + self.headways.get(line, 0), "transfer"))
            object.__setattr__(self, "_adj", adj) if False else setattr(self, "_adj", adj)
        return self._adj

    def _dijkstra(self, seeds: dict[int, int]
                  ) -> tuple[dict[int, int], dict[int, tuple[int, str]]]:
        """seeds — «станция → уже потраченные секунды» (пешее плечо до входа).

        Несколько источников за один обход: у точки ближайших платформ три, и
        каждая входит в очередь со своим плечом. Минимум по вариантам входа
        получается сам собой, а не отдельным сравнением снаружи.
        """
        adj = self._adjacency()
        dist: dict[int, int] = {}
        prev: dict[int, tuple[int, str]] = {}
        heap: list[tuple[int, int]] = []
        for sid, walk in seeds.items():
            if sid not in self.stations:
                continue
            # посадка на первую линию тоже стоит интервала
            start = walk + self.headways.get(self.stations[sid].line_ref, 0)
            if sid not in dist or start < dist[sid]:
                dist[sid] = start
                heapq.heappush(heap, (start, sid))
        while heap:
            d, node = heapq.heappop(heap)
            if d > dist.get(node, d):
                continue
            for nxt, sec, kind in adj.get(node, ()):
                nd = d + sec
                if nd < dist.get(nxt, nd + 1):
                    dist[nxt] = nd
                    prev[nxt] = (node, kind)
                    heapq.heappush(heap, (nd, nxt))
        return dist, prev

    def times_from(self, seeds: dict[int, int]) -> dict[int, int]:
        """Секунды до каждой достижимой платформы. Один обход one-to-all."""
        dist, _ = self._dijkstra(seeds)
        return dist

    def route(self, seeds: dict[int, int],
              targets: dict[int, int]) -> MetroRoute | None:
        """Лучший маршрут между наборами входов и выходов, с разбивкой."""
        if not seeds or not targets:
            return None
        dist, prev = self._dijkstra(seeds)
        reachable = [(dist[t] + walk, t) for t, walk in targets.items()
                     if t in dist]
        if not reachable:
            return None
        total, end = min(reachable)

        path: list[int] = [end]
        kinds: list[str] = []
        while path[-1] in prev:
            node, kind = prev[path[-1]]
            kinds.append(kind)
            path.append(node)
        path.reverse()
        kinds.reverse()

        route = MetroRoute(ride_seconds=total)
        run_start = path[0]
        run_stops = 0
        for i, kind in enumerate(kinds):
            a, b = path[i], path[i + 1]
            if kind == "ride":
                run_stops += 1
                if self.edges[(a, b)][1]:
                    route.estimated = True
                continue
            # пересадка закрывает текущий отрезок
            if run_stops:
                route.segments.append(self._segment(run_start, a, run_stops))
            sec, est, outdoor = self.transfers[(a, b)]
            route.transfers.append(Transfer(
                from_station=self.stations[a].name,
                to_station=self.stations[b].name,
                seconds=sec, estimated=est, outdoor=outdoor))
            route.estimated = route.estimated or est
            run_start, run_stops = b, 0
        if run_stops:
            route.segments.append(self._segment(run_start, path[-1], run_stops))
        return route

    def _segment(self, a: int, b: int, stops: int) -> Segment:
        st = self.stations[a]
        seconds = 0
        return Segment(line_ref=st.line_ref, line_name=st.line_name,
                       system=st.system, colour=st.colour,
                       from_station=st.name, to_station=self.stations[b].name,
                       stops=stops, seconds=seconds,
                       estimated=False)


#: Кэш графа на процесс: ключ — (город, отпечаток свежести графа).
_GRAPH_CACHE: dict[tuple[str, str], MetroGraph] = {}


def clear_graph_cache() -> None:
    _GRAPH_CACHE.clear()


def _fingerprint(conn: psycopg.Connection, city: str) -> str:
    row = conn.execute("""
        SELECT COALESCE(max(updated_at)::text,'-') || ':' || count(*)
        FROM metro_station WHERE city = %s;""", (city,)).fetchone()
    return row[0] if row else "-"


def load_graph(conn: psycopg.Connection, city: str) -> MetroGraph | None:
    """Граф города из БД с кэшем на процесс. None — графа для города нет.

    Инвалидация по отпечатку (max updated_at + число станций): пересборка
    графа меняет его, и следующий запрос перечитает таблицы сам.
    """
    key = (city, _fingerprint(conn, city))
    if key in _GRAPH_CACHE:
        return _GRAPH_CACHE[key]

    rows = conn.execute("""
        SELECT s.id, s.name, ml.ref, ml.name, ml.system, ml.colour,
               ST_X(s.geom), ST_Y(s.geom), ml.headway_s
        FROM metro_station s JOIN metro_line ml ON ml.id = s.line_id
        WHERE s.city = %s;""", (city,)).fetchall()
    if not rows:
        return None

    stations, headways = {}, {}
    for sid, name, ref, lname, system, colour, lon, lat, headway in rows:
        stations[sid] = Station(sid, name, ref, lname, system, colour, lon, lat)
        headways[ref] = headway

    edges = {(a, b): (sec, est) for a, b, sec, est in conn.execute(
        "SELECT from_station, to_station, seconds, estimated FROM metro_edge "
        "WHERE city = %s;", (city,)).fetchall()}
    transfers = {(a, b): (sec, est, out) for a, b, sec, est, out in conn.execute(
        "SELECT from_station, to_station, seconds, estimated, outdoor "
        "FROM metro_transfer WHERE city = %s;", (city,)).fetchall()}

    graph = MetroGraph(stations=stations, edges=edges, transfers=transfers,
                       headways=headways)
    _GRAPH_CACHE.clear()          # держим только актуальный отпечаток
    _GRAPH_CACHE[key] = graph
    return graph
```

- [ ] **Step 4: Посчитать секунды отрезка**

Тест `test_direct_ride_on_one_line` требует, чтобы `Segment.seconds` был осмысленным. Заменить `_segment` на версию, суммирующую рёбра вдоль отрезка:

```python
    def _segment(self, a: int, b: int, stops: int) -> Segment:
        st = self.stations[a]
        seconds, estimated, node = 0, False, a
        for _ in range(stops):
            nxt = next((n for (f, n), _ in self.edges.items() if f == node
                        and self.stations[n].line_ref == st.line_ref
                        and self.stations[n].id != node), None)
            if nxt is None:
                break
            sec, est = self.edges[(node, nxt)]
            seconds += sec
            estimated = estimated or est
            node = nxt
        return Segment(line_ref=st.line_ref, line_name=st.line_name,
                       system=st.system, colour=st.colour,
                       from_station=st.name, to_station=self.stations[b].name,
                       stops=stops, seconds=seconds, estimated=estimated)
```

Этот вариант выбирает соседа неоднозначно на кольцевых линиях. Правильнее протащить фактический путь: изменить сигнатуру на `_segment(self, path_slice: list[int], stops: int)` и вызывать её со срезом `path`, суммируя `self.edges[(path_slice[i], path_slice[i+1])]`. Реализовать именно так — хранить срез пути при разборе `kinds`.

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_metro_route.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/online/metro_route.py tests/test_metro_route.py
git commit -m "feat: Дейкстра по графу рельсового транспорта"
```

---

## Task 10: Контракт MetroRide

**Files:**
- Modify: `habitus/online/schema.py:126-140` (`PointConstraint`), `:175-183` (`RouteLeg`)
- Test: `tests/test_online_schema.py`

**Interfaces:**
- Consumes: ничего (модели независимы от движка).
- Produces:
  - `MetroSystem = Literal["subway", "mck", "mcd"]`
  - `class MetroSegment(BaseModel)`: `line_ref: str`, `line_name: str`, `system: MetroSystem`, `colour: str | None`, `from_station: str`, `to_station: str`, `stops: int`, `minutes: int`, `estimated: bool = False`
  - `class MetroTransfer(BaseModel)`: `from_station: str`, `to_station: str`, `minutes: int`, `outdoor: bool = False`, `estimated: bool = False`
  - `class MetroRide(BaseModel)`: `walk_from_home_min: int`, `walk_to_dest_min: int`, `segments: list[MetroSegment]`, `transfers: list[MetroTransfer]`, `total_minutes: int`, `estimated: bool = False`
  - `RouteLeg.metro: MetroRide | None = None`
  - `PointConstraint.mode` принимает `"metro"`

- [ ] **Step 1: Написать падающий тест**

Добавить в `tests/test_online_schema.py`:

```python
import pytest
from pydantic import ValidationError

from habitus.online.schema import (LineStringGeometry, MetroRide, MetroSegment,
                                   MetroTransfer, PointConstraint, RouteLeg)


def _ride(**over):
    base = dict(
        walk_from_home_min=7, walk_to_dest_min=5,
        segments=[MetroSegment(line_ref="1", line_name="Сокольническая",
                               system="subway", colour="#EF161E",
                               from_station="Сокольники", to_station="Охотный Ряд",
                               stops=6, minutes=13)],
        transfers=[], total_minutes=25)
    base.update(over)
    return MetroRide(**base)


def test_point_constraint_accepts_metro_mode():
    assert PointConstraint(lon=37.6, lat=55.75, minutes=40, mode="metro").mode == "metro"


def test_point_constraint_rejects_unknown_mode():
    with pytest.raises(ValidationError):
        PointConstraint(lon=37.6, lat=55.75, minutes=40, mode="teleport")


def test_route_leg_metro_is_optional():
    leg = RouteLeg(to_label="офис", to_kind="work", mode="walk",
                   depart="08:00", arrive="08:30", minutes=30, safety="safe",
                   geometry=LineStringGeometry(coordinates=[(37.6, 55.7), (37.61, 55.71)]))
    assert leg.metro is None


def test_segment_rejects_unknown_system():
    with pytest.raises(ValidationError):
        MetroSegment(line_ref="1", line_name="л", system="tram", colour=None,
                     from_station="A", to_station="B", stops=2, minutes=5)


def test_estimated_defaults_to_false_everywhere():
    ride = _ride()
    assert ride.estimated is False
    assert ride.segments[0].estimated is False
    assert MetroTransfer(from_station="A", to_station="B", minutes=3).outdoor is False


def test_ride_total_is_the_door_to_door_number():
    # RouteLeg.minutes — итог, MetroRide — его разбивка; фронт не складывает заново
    ride = _ride()
    leg = RouteLeg(to_label="офис", to_kind="work", mode="metro",
                   depart="08:00", arrive="08:25", minutes=ride.total_minutes,
                   safety="safe",
                   geometry=LineStringGeometry(coordinates=[(37.6, 55.7), (37.61, 55.71)]),
                   metro=ride)
    assert leg.minutes == leg.metro.total_minutes == 25
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_online_schema.py -v`
Expected: FAIL — `ImportError: cannot import name 'MetroRide'`

- [ ] **Step 3: Реализовать**

В `habitus/online/schema.py` добавить рядом с остальными Literal'ами:

```python
MetroSystem = Literal["subway", "mck", "mcd"]
```

Перед `class RouteLeg` добавить модели:

```python
class MetroSegment(BaseModel):
    """Отрезок поездки по одной линии без пересадок."""
    line_ref: str
    line_name: str
    system: MetroSystem
    colour: str | None = None
    from_station: str
    to_station: str
    stops: int = Field(ge=1)
    minutes: int = Field(ge=0)
    # true — время выведено из расстояния, а не взято из курируемого файла.
    # Признак едет до фронта: оценка показывается как оценка.
    estimated: bool = False


class MetroTransfer(BaseModel):
    from_station: str
    to_station: str
    minutes: int = Field(ge=0)
    # Переход улицей (типично между метро и МЦД) — рисуется отдельным пешим
    # сегментом, а не сливается в общий «переход»: он вдвое-втрое длиннее.
    outdoor: bool = False
    estimated: bool = False


class MetroRide(BaseModel):
    """Разбивка метро-ноги. Итог «от двери до двери» живёт в RouteLeg.minutes,
    здесь — из чего он сложился. Сумма частей равна RouteLeg.minutes; фронт
    показывает разбивку и не складывает её заново, иначе округления разойдутся."""
    walk_from_home_min: int = Field(ge=0)
    walk_to_dest_min: int = Field(ge=0)
    segments: list[MetroSegment] = []
    transfers: list[MetroTransfer] = []
    total_minutes: int = Field(ge=0)
    estimated: bool = False
```

В `RouteLeg` добавить поле:

```python
    # Разбивка поездки на рельсовом транспорте. None у ног любого другого
    # режима — существующие потребители RouteLeg не ломаются.
    metro: MetroRide | None = None
```

В `PointConstraint` расширить `mode`:

```python
    # "metro" считается внутренним движком по графу, остальные — изохронами ORS
    mode: Literal["foot-walking", "cycling-regular", "driving-car", "metro"] = "foot-walking"
```

- [ ] **Step 4: Запустить тесты**

Run: `uv run pytest tests/test_online_schema.py tests/test_schema.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/online/schema.py tests/test_online_schema.py
git commit -m "feat: контракт MetroRide и режим metro в PointConstraint"
```

---

## Task 11: Метро-нога в досье

**Files:**
- Modify: `habitus/online/dossier.py:33-35` (`ROUTE_PROFILE`), `:125-161` (`_family_data`), `:440-441` (гейт города)
- Modify: `habitus/online/metro_route.py` (добавить `nearest_stations`, `door_to_door`)
- Test: `tests/test_dossier.py`

**Interfaces:**
- Consumes: `MetroGraph`, `load_graph` (Задача 9); `MetroRide`, `MetroSegment`, `MetroTransfer` (Задача 10); `straight_walk_seconds` (Задача 7).
- Produces:
  - `nearest_stations(conn, city: str, lon: float, lat: float, k: int = 3, walker=None) -> dict[int, int]` — «id платформы → пешие секунды»
  - `door_to_door(conn, city, home: tuple[float,float], dest: tuple[float,float], walker=None) -> tuple[MetroRide, list[list[float]]] | None` — возвращает разбивку и геометрию для карты

- [ ] **Step 1: Написать падающий тест**

Добавить в `tests/test_dossier.py`:

```python
from habitus.online.dossier import ROUTE_PROFILE, build_dossier
from habitus.online.schema import (DossierRequest, HouseholdLegIntent,
                                   HouseholdMemberIntent, ParsedQuery)


def test_metro_is_no_longer_an_unroutable_mode():
    # раньше ROUTE_PROFILE не знал metro и _family_data молча выбрасывал ногу
    assert "metro" in ROUTE_PROFILE


class _StubMetro:
    """Подменяет движок: досье не должно ходить в БД ради этого теста."""
    def __init__(self, ride, geometry):
        self.ride, self.geometry = ride, geometry
        self.calls = 0

    def __call__(self, conn, city, home, dest, walker=None):
        self.calls += 1
        return self.ride, self.geometry


def test_metro_leg_carries_the_ride_breakdown(monkeypatch, dossier_conn):
    from habitus.online import dossier as mod
    from habitus.online.schema import MetroRide, MetroSegment

    ride = MetroRide(
        walk_from_home_min=7, walk_to_dest_min=5, total_minutes=25,
        segments=[MetroSegment(line_ref="1", line_name="Сокольническая",
                               system="subway", colour="#EF161E",
                               from_station="Сокольники", to_station="Охотный Ряд",
                               stops=6, minutes=13)])
    stub = _StubMetro(ride, [[37.60, 55.75], [37.62, 55.76]])
    monkeypatch.setattr(mod, "door_to_door", stub)

    req = DossierRequest(
        object_id="A", city="msk",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    payload = build_dossier(req, dossier_conn, geocoder=lambda q: (37.62, 55.76))
    block = next(b for b in payload.blocks if b.key == "family_routing")
    leg = block.data.members[0].legs[0]
    assert leg.mode == "metro"
    assert leg.metro is not None
    assert leg.minutes == leg.metro.total_minutes == 25
    assert leg.arrive == "08:25"


def test_no_graph_for_city_drops_the_block_instead_of_showing_zeros(
        monkeypatch, dossier_conn):
    from habitus.online import dossier as mod
    monkeypatch.setattr(mod, "door_to_door", lambda *a, **kw: None)

    req = DossierRequest(
        object_id="A", city="spb",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    payload = build_dossier(req, dossier_conn, geocoder=lambda q: (30.3, 59.93))
    # синтетический ноль вместо отсутствующего замера запрещён
    assert not any(b.key == "family_routing" for b in payload.blocks)
```

Фикстуру `dossier_conn` взять из существующих тестов `tests/test_dossier.py` — если её там нет, создать по образцу фикстуры `conn` из `tests/test_metro_access_db.py`, добавив одну строку в `listings` с `external_id='A'`.

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_dossier.py -v`
Expected: FAIL — `assert "metro" in ROUTE_PROFILE`

- [ ] **Step 3: Реализовать движок «дверь-дверь»**

Дописать в `habitus/online/metro_route.py`:

```python
from habitus.online.schema import MetroRide, MetroSegment, MetroTransfer


def nearest_stations(conn: psycopg.Connection, city: str, lon: float, lat: float,
                     k: int = 3, walker=None) -> dict[int, int]:
    """«id платформы → пешие секунды». Три платформы, а не одна: ближайшая по
    прямой регулярно стоит на тупиковой ветке."""
    from habitus.geo.metro_access import straight_walk_seconds

    rows = conn.execute("""
        SELECT s.id, ST_X(s.geom), ST_Y(s.geom),
               ST_Distance(s.geom::geography,
                           ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography)
        FROM (SELECT id, geom FROM metro_station WHERE city = %s
              ORDER BY geom <-> ST_SetSRID(ST_MakePoint(%s,%s),4326)
              LIMIT %s) s
        ORDER BY 4 LIMIT %s;""",
        (lon, lat, city, lon, lat, max(k * 3, 9), k)).fetchall()

    out: dict[int, int] = {}
    for sid, s_lon, s_lat, metres in rows:
        seconds = straight_walk_seconds(metres)
        if walker is not None:
            try:
                got = walker((lon, lat), (s_lon, s_lat))
                if got is not None:
                    seconds = int(round(got))
            except Exception:  # noqa: BLE001 — внешний роутер, деградируем к оценке
                pass
        out[sid] = seconds
    return out


def door_to_door(conn: psycopg.Connection, city: str,
                 home: tuple[float, float], dest: tuple[float, float],
                 walker=None) -> tuple[MetroRide, list[list[float]]] | None:
    """Разбивка поездки от дома до цели и её геометрия для карты.

    None — графа города нет либо цель недостижима. Блок тогда деградирует до
    отсутствия: синтетический ноль вместо отсутствующего замера запрещён.
    """
    graph = load_graph(conn, city)
    if graph is None:
        return None
    seeds = nearest_stations(conn, city, home[0], home[1], walker=walker)
    targets = nearest_stations(conn, city, dest[0], dest[1], walker=walker)
    if not seeds or not targets:
        return None
    route = graph.route(seeds, targets)
    if route is None:
        return None

    def _min(seconds: int) -> int:
        return max(1, int(round(seconds / 60)))

    entry = graph.stations[min(seeds, key=lambda s: seeds[s])]
    home_walk = _min(min(seeds.values()))
    dest_walk = _min(min(targets.values()))

    ride = MetroRide(
        walk_from_home_min=home_walk, walk_to_dest_min=dest_walk,
        total_minutes=_min(route.ride_seconds + min(targets.values())),
        estimated=route.estimated,
        segments=[MetroSegment(
            line_ref=s.line_ref, line_name=s.line_name, system=s.system,
            colour=s.colour, from_station=s.from_station,
            to_station=s.to_station, stops=s.stops, minutes=_min(s.seconds),
            estimated=s.estimated) for s in route.segments],
        transfers=[MetroTransfer(
            from_station=t.from_station, to_station=t.to_station,
            minutes=_min(t.seconds), outdoor=t.outdoor,
            estimated=t.estimated) for t in route.transfers])

    # Геометрия для карты: дом → станция входа → станции пути → цель.
    geometry: list[list[float]] = [[home[0], home[1]], [entry.lon, entry.lat]]
    for seg in route.segments:
        for st in graph.stations.values():
            if st.name == seg.to_station and st.line_ref == seg.line_ref:
                geometry.append([st.lon, st.lat])
                break
    geometry.append([dest[0], dest[1]])
    return ride, geometry
```

- [ ] **Step 4: Подключить в досье**

В `habitus/online/dossier.py` расширить `ROUTE_PROFILE` и импортировать движок:

```python
from habitus.online.metro_route import door_to_door

ROUTE_PROFILE = {
    "walk": "foot-walking", "scooter": "cycling-regular",
    "car": "driving-car",
    # metro считается внутренним движком по графу, а не ORS: публичный ORS
    # public transport не умеет (см. ORSProvider.directions).
    "metro": "metro",
}
```

В `_family_data` добавить ветку метро перед вызовом ORS, внутри цикла по `member_intent.legs`, сразу после получения `target`:

```python
            if intent.mode == "metro":
                got = door_to_door(conn, req.city, start, target)
                if got is None:
                    # графа города нет или цель недостижима — ногу пропускаем,
                    # нулей не выдумываем
                    continue
                ride, geometry = got
                minutes = ride.total_minutes
                depart = intent.depart or _minutes_to_clock(intent.arrive, -minutes)
                arrive = intent.arrive or _minutes_to_clock(intent.depart, minutes)
                legs.append(RouteLeg(
                    to_label=intent.to_label, to_kind=intent.to_kind,
                    mode="metro", depart=depart, arrive=arrive,
                    minutes=minutes, safety="safe", metro=ride,
                    geometry=LineStringGeometry(coordinates=[tuple(p) for p in geometry])))
                start = target
                continue
```

Сигнатура `_family_data` получает `conn` первым параметром; поправить её вызов в `build_dossier`.

Снять гейт города с маршрутного блока: строку 441 заменить на

```python
    # Маршрутный блок больше не московский: граф Петербурга — такой же граф.
    # Блоки social и climate остаются под is_moscow — под них нет данных по
    # Петербургу, и это отдельная задача.
    family = _family_data(conn, req, listing, route_provider, geocoder)
```

Проверку `_inside_moscow` в `_family_data` (строка 144) применять только к немаршрутным по метро ногам — для метро границей служит сам граф, а он у Москвы шире города (МЦД идут в область):

```python
            if target is None:
                continue
            if intent.mode != "metro" and req.city == "msk" and not _inside_moscow(target):
                continue
```

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_dossier.py tests/test_metro_route.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/online/dossier.py habitus/online/metro_route.py tests/test_dossier.py
git commit -m "feat: метро-нога в досье с разбивкой поездки"
```

---

# Этап C — поиск

## Task 12: SQL-фильтр по времени на метро

**Files:**
- Modify: `habitus/online/metro_route.py` (добавить `metro_predicate`)
- Modify: `habitus/online/orchestrator.py:49-53`
- Create: `tests/test_metro_search_db.py`

**Interfaces:**
- Consumes: `load_graph`, `nearest_stations`, `times_from` (Задачи 9, 11); `listing_metro_access` (Задача 7).
- Produces: `metro_predicate(conn, city: str, lon: float, lat: float, minutes: int) -> tuple[str, tuple] | None`

- [ ] **Step 1: Написать падающий тест**

Создать `tests/test_metro_search_db.py`:

```python
import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.metro_route import clear_graph_cache, metro_predicate


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        clear_graph_cache()
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings CASCADE;")
            cur.execute("TRUNCATE metro_line CASCADE;")
            cur.execute("""INSERT INTO metro_line
                (id, city, system, ref, name, headway_s, fallback_speed_kmh)
                VALUES (1,'msk','subway','1','л1',120,40);""")
            # A —600с— B —600с— C, цель у C
            cur.execute("""INSERT INTO metro_station
                (id, city, line_id, name, name_norm, geom, order_index) VALUES
                (10,'msk',1,'A','a',ST_SetSRID(ST_MakePoint(37.50,55.75),4326),0),
                (11,'msk',1,'B','b',ST_SetSRID(ST_MakePoint(37.60,55.75),4326),1),
                (12,'msk',1,'C','c',ST_SetSRID(ST_MakePoint(37.70,55.75),4326),2);""")
            cur.execute("""INSERT INTO metro_edge
                (city, from_station, to_station, seconds) VALUES
                ('msk',10,11,600),('msk',11,10,600),
                ('msk',11,12,600),('msk',12,11,600);""")
            # BLIZKO живёт у B (10 мин езды до C), DALEKO — у A (20 мин)
            for eid, lon in (("BLIZKO", 37.60), ("DALEKO", 37.50)):
                cur.execute("""INSERT INTO listings
                    (external_id, source, is_active, city, geom)
                    VALUES (%s,'test',TRUE,'msk',
                            ST_SetSRID(ST_MakePoint(%s,55.75),4326));""", (eid, lon))
            cur.execute("""INSERT INTO listing_metro_access
                (external_id, station_id, walk_seconds) VALUES
                ('BLIZKO',11,120),('DALEKO',10,120);""")
        c.commit()
        yield c


def _ids(conn, sql, params) -> set[str]:
    rows = conn.execute(
        f"SELECT external_id FROM listings WHERE {sql}", params).fetchall()
    return {r[0] for r in rows}


def test_predicate_keeps_only_listings_within_the_budget(conn):
    # цель — у станции C; 15 минут хватает от B, но не от A
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)
    assert _ids(conn, sql, params) == {"BLIZKO"}


def test_wider_budget_admits_the_far_one(conn):
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=40)
    assert _ids(conn, sql, params) == {"BLIZKO", "DALEKO"}


def test_walk_leg_counts_towards_the_budget(conn):
    # плечо DALEKO раздуто до 20 минут — в 40-минутный бюджет он больше не лезет
    conn.execute("UPDATE listing_metro_access SET walk_seconds = 1200 "
                 "WHERE external_id = 'DALEKO';")
    conn.commit()
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=40)
    assert _ids(conn, sql, params) == {"BLIZKO"}


def test_no_graph_for_city_returns_none(conn):
    assert metro_predicate(conn, "spb", 30.3, 59.93, minutes=40) is None


def test_predicate_is_fully_parameterized(conn):
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)
    # никакой склейки значений в текст запроса — только плейсхолдеры
    assert "%s" in sql and str(15 * 60) not in sql
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_metro_search_db.py -v`
Expected: FAIL — `ImportError: cannot import name 'metro_predicate'`

- [ ] **Step 3: Реализовать предикат**

Дописать в `habitus/online/metro_route.py`:

```python
def metro_predicate(conn: psycopg.Connection, city: str, lon: float, lat: float,
                    minutes: int) -> tuple[str, tuple] | None:
    """SQL-предикат «доехать до точки на метро не дольше N минут».

    Обход графа делает Python (SQL не умеет Дейкстру без pgRouting), а его
    результат уезжает в запрос VALUES-джойном. Тот же механизм extra_sql, что у
    изохронного фильтра, — поэтому relaxation-петля подхватывает ограничение
    как обычную клаузу, а constraint_diagnostics показывает его вклад в пустую
    выдачу без доработок.

    None — графа города нет: фильтровать нечем, и молча выкидывать всю выдачу
    нельзя.
    """
    graph = load_graph(conn, city)
    if graph is None:
        return None
    targets = nearest_stations(conn, city, lon, lat)
    if not targets:
        return None
    times = graph.times_from(targets)
    if not times:
        return None

    # Время симметрично: обход от цели даёт время «станция → цель», включая
    # пешее плечо до самой цели (оно зашито в seeds).
    pairs = list(times.items())
    values = ",".join(["(%s::bigint,%s::int)"] * len(pairs))
    params: list = []
    for station_id, seconds in pairs:
        params.extend([station_id, seconds])
    params.append(minutes * 60)

    sql = (f"external_id IN (SELECT a.external_id FROM listing_metro_access a "
           f"JOIN (VALUES {values}) AS t(station_id, seconds) "
           f"ON t.station_id = a.station_id "
           f"GROUP BY a.external_id "
           f"HAVING min(a.walk_seconds + t.seconds) <= %s)")
    return sql, tuple(params)
```

- [ ] **Step 4: Подключить в orchestrator**

В `habitus/online/orchestrator.py` заменить блок построения `base_sql` (строки 49–53):

```python
    base_sql, base_params = None, []
    if point is not None:
        if point.mode == "metro":
            # Метро считает внутренний движок по графу: изохроны ORS для
            # public transport непригодны (см. ORSProvider.directions).
            # Графа для города нет → ограничение не накладывается вовсе:
            # молча обнулять выдачу нельзя, а врать оценкой — тем более.
            got = metro_predicate(conn, city or "msk", point.lon, point.lat,
                                  point.minutes)
            if got is not None:
                base_sql, base_params = got[0], list(got[1])
        else:
            s, p = point_predicate(point.lon, point.lat, point.minutes,
                                   provider, point.mode)
            base_sql, base_params = s, list(p)
```

Добавить импорт `from habitus.online.metro_route import metro_predicate`.

- [ ] **Step 5: Запустить тесты**

Run: `uv run pytest tests/test_metro_search_db.py tests/test_orchestrator.py -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add habitus/online/metro_route.py habitus/online/orchestrator.py tests/test_metro_search_db.py
git commit -m "feat: фильтр выдачи по времени поездки на метро"
```

---

## Task 13: NLU — «без машины», «40 минут на метро»

**Files:**
- Modify: `habitus/online/nlu.py:15-65` (`SYSTEM_PROMPT`)
- Test: `tests/test_nlu.py`

**Interfaces:**
- Consumes: `PointConstraint.mode == "metro"` (Задача 10).
- Produces: изменений в сигнатурах нет — меняется только промпт и его примеры.

- [ ] **Step 1: Написать падающий тест**

Добавить в `tests/test_nlu.py`:

```python
from habitus.online.nlu import SYSTEM_PROMPT


def test_prompt_teaches_the_metro_travel_budget():
    # без этого «40 минут до работы на метро» уезжает в semantic_text
    assert "метро" in SYSTEM_PROMPT
    assert "point" in SYSTEM_PROMPT
    assert '"mode": "metro"' in SYSTEM_PROMPT


def test_prompt_mentions_carless_phrasing():
    assert "без машины" in SYSTEM_PROMPT
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `uv run pytest tests/test_nlu.py -v`
Expected: FAIL — `assert '"mode": "metro"' in SYSTEM_PROMPT`

- [ ] **Step 3: Реализовать**

В `habitus/online/nlu.py` дописать в `SYSTEM_PROMPT` перед блоком примеров:

```
- Бюджет поездки на метро («40 минут до работы на метро», «без машины, час до \
центра») — это не household, а отдельная поездка до места: назови место в \
to_label ноги household с mode "metro". Если места нет, а есть только режим \
(«без машины»), ставь mode "metro" у уже названных поездок, ничего не выдумывая.
```

и добавить пример в блок примеров:

```
Запрос: «двушка, без машины, до Сити не больше 40 минут на метро»
→ {"rooms": [2], "household": [{"id": "me", "label": "я", "legs": \
[{"to_label": "Москва-Сити", "to_kind": "work", "mode": "metro"}]}]}
```

- [ ] **Step 4: Запустить тесты**

Run: `uv run pytest tests/test_nlu.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add habitus/online/nlu.py tests/test_nlu.py
git commit -m "feat: разбор бюджета поездки на метро в NLU"
```

---

# Этап D — Go-шлюз

## Task 14: Passthrough MetroRide

**Files:**
- Modify: `backend/internal/service/object_service.go:176-200`
- Test: `backend/internal/service/object_dossier_contract_test.go`

**Interfaces:**
- Consumes: JSON-форму `MetroRide` из Задачи 10.
- Produces: Go-структуры `MetroSegment`, `MetroTransfer`, `MetroRide`, поле `FamilyRouteLeg.Metro *MetroRide`.

- [ ] **Step 1: Написать падающий тест**

Добавить в `backend/internal/service/object_dossier_contract_test.go`:

```go
func TestMetroRideSurvivesPassthrough(t *testing.T) {
	raw := []byte(`{"to_label":"офис","to_kind":"work","mode":"metro",
		"depart":"08:00","arrive":"08:25","minutes":25,"safety":"safe",
		"geometry":{"type":"LineString","coordinates":[[37.6,55.75],[37.62,55.76]]},
		"metro":{"walk_from_home_min":7,"walk_to_dest_min":5,"total_minutes":25,
			"estimated":false,
			"segments":[{"line_ref":"1","line_name":"Сокольническая",
				"system":"subway","colour":"#EF161E","from_station":"Сокольники",
				"to_station":"Охотный Ряд","stops":6,"minutes":13,"estimated":false}],
			"transfers":[{"from_station":"Охотный Ряд","to_station":"Театральная",
				"minutes":3,"outdoor":true,"estimated":false}]}}`)

	var leg FamilyRouteLeg
	if err := json.Unmarshal(raw, &leg); err != nil {
		t.Fatalf("нога не разобралась: %v", err)
	}
	if leg.Metro == nil {
		t.Fatal("разбивка поездки потеряна")
	}
	if leg.Metro.TotalMinutes != leg.Minutes {
		t.Fatalf("итог разошёлся с разбивкой: %d против %d",
			leg.Metro.TotalMinutes, leg.Minutes)
	}
	if len(leg.Metro.Segments) != 1 || leg.Metro.Segments[0].System != "subway" {
		t.Fatalf("сегмент потерян: %#v", leg.Metro.Segments)
	}
	if !leg.Metro.Transfers[0].Outdoor {
		t.Fatal("признак уличной пересадки потерян")
	}

	back, err := json.Marshal(leg)
	if err != nil {
		t.Fatalf("обратная сериализация: %v", err)
	}
	if !bytes.Contains(back, []byte(`"outdoor":true`)) {
		t.Fatalf("признак не доехал наружу: %s", back)
	}
}

func TestNonMetroLegHasNoMetroField(t *testing.T) {
	raw := []byte(`{"to_label":"школа","to_kind":"school","mode":"walk",
		"depart":"08:00","arrive":"08:15","minutes":15,"safety":"safe",
		"geometry":{"type":"LineString","coordinates":[[37.6,55.75],[37.61,55.75]]}}`)
	var leg FamilyRouteLeg
	if err := json.Unmarshal(raw, &leg); err != nil {
		t.Fatalf("нога не разобралась: %v", err)
	}
	if leg.Metro != nil {
		t.Fatal("у пешей ноги не должно быть разбивки метро")
	}
	back, _ := json.Marshal(leg)
	if bytes.Contains(back, []byte(`"metro"`)) {
		t.Fatalf("пустое поле не должно уезжать наружу: %s", back)
	}
}
```

Добавить `"bytes"` и `"encoding/json"` в импорты файла, если их там нет.

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `cd backend && go test ./internal/service/ -run Metro -v`
Expected: FAIL — `leg.Metro undefined`

- [ ] **Step 3: Реализовать**

В `backend/internal/service/object_service.go` рядом с `FamilyRouteLeg` добавить:

```go
// MetroSystem — enum, зафиксированный на трёх сторонах:
// habitus/online/schema.py ↔ здесь ↔ frontend/lib/agent/types.ts.
type MetroSystem string

const (
	SystemSubway MetroSystem = "subway"
	SystemMCK    MetroSystem = "mck"
	SystemMCD    MetroSystem = "mcd"
)

type MetroSegment struct {
	LineRef     string      `json:"line_ref"`
	LineName    string      `json:"line_name"`
	System      MetroSystem `json:"system"`
	Colour      *string     `json:"colour"`
	FromStation string      `json:"from_station"`
	ToStation   string      `json:"to_station"`
	Stops       int         `json:"stops"`
	Minutes     int         `json:"minutes"`
	// Время выведено из расстояния, а не взято из курируемого файла.
	Estimated bool `json:"estimated"`
}

type MetroTransfer struct {
	FromStation string `json:"from_station"`
	ToStation   string `json:"to_station"`
	Minutes     int    `json:"minutes"`
	// Переход улицей (типично метро↔МЦД) — вдвое-втрое длиннее подземного.
	Outdoor   bool `json:"outdoor"`
	Estimated bool `json:"estimated"`
}

// MetroRide — разбивка метро-ноги. Итог «от двери до двери» живёт в
// FamilyRouteLeg.Minutes, здесь — из чего он сложился.
type MetroRide struct {
	WalkFromHomeMin int             `json:"walk_from_home_min"`
	WalkToDestMin   int             `json:"walk_to_dest_min"`
	Segments        []MetroSegment  `json:"segments"`
	Transfers       []MetroTransfer `json:"transfers"`
	TotalMinutes    int             `json:"total_minutes"`
	Estimated       bool            `json:"estimated"`
}
```

В `FamilyRouteLeg` добавить поле:

```go
	// Разбивка поездки на рельсовом транспорте; nil у ног любого другого режима.
	Metro *MetroRide `json:"metro,omitempty"`
```

- [ ] **Step 4: Запустить тесты**

Run: `cd backend && go test ./... `
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/object_service.go backend/internal/service/object_dossier_contract_test.go
git commit -m "feat: passthrough разбивки метро-поездки через шлюз"
```

---

## Task 15: Линии метро в слое карты

**Files:**
- Create: `backend/internal/repository/metro_repo.go`
- Modify: `backend/internal/service/geo_layers_service.go:20-26`
- Test: `backend/internal/service/geo_layers_service_test.go`

**Interfaces:**
- Consumes: `metro_line`, `metro_line_geom` (Задачи 5, 6).
- Produces:
  - `domain.MetroLine{Ref, Name, System string; Colour string; GeometryJSON string}` в `backend/internal/domain/domain.go`
  - `(*MetroRepo).ListLines(ctx, city string) ([]domain.MetroLine, error)`
  - `GeoLayersService` получает поле `metro metroLister` и отдаёт LineString-фичи в слое `metro`.

- [ ] **Step 1: Написать падающий тест**

Добавить в `backend/internal/service/geo_layers_service_test.go`:

```go
type fakeMetroLister struct{ lines []domain.MetroLine }

func (f fakeMetroLister) ListLines(ctx context.Context, city string) ([]domain.MetroLine, error) {
	return f.lines, nil
}

func TestMetroLayerCarriesLinesWithSystemAndColour(t *testing.T) {
	svc := NewGeoLayersService(
		fakePOILister{pois: []domain.POI{{Kind: "metro", Name: "Сокольники",
			Lon: 37.68, Lat: 55.79}}},
		fakeEvidenceLister{}, fakeListingLister{},
		fakeMetroLister{lines: []domain.MetroLine{{
			Ref: "D1", Name: "МЦД-1", System: "mcd", Colour: "#F6A800",
			GeometryJSON: `{"type":"LineString","coordinates":[[37.5,55.7],[37.6,55.8]]}`,
		}}})

	fc, err := svc.Layer(context.Background(), "msk", "metro", nil)
	if err != nil {
		t.Fatalf("слой не собрался: %v", err)
	}

	var points, lines int
	for _, f := range fc.Features {
		switch f.Geometry.Type {
		case "Point":
			points++
		case "LineString":
			lines++
			// палитра не зашивается на фронте — цвет и система едут в properties
			if f.Properties["system"] != "mcd" {
				t.Fatalf("система не доехала: %#v", f.Properties)
			}
			if f.Properties["colour"] != "#F6A800" {
				t.Fatalf("цвет не доехал: %#v", f.Properties)
			}
		}
	}
	if points != 1 || lines != 1 {
		t.Fatalf("ожидались точка и линия, получено %d и %d", points, lines)
	}
}

func TestOtherLayersHaveNoMetroLines(t *testing.T) {
	svc := NewGeoLayersService(
		fakePOILister{pois: []domain.POI{{Kind: "park", Name: "Сокольники",
			Lon: 37.68, Lat: 55.79}}},
		fakeEvidenceLister{}, fakeListingLister{},
		fakeMetroLister{lines: []domain.MetroLine{{Ref: "1", System: "subway",
			GeometryJSON: `{"type":"LineString","coordinates":[[37.5,55.7],[37.6,55.8]]}`}}})

	fc, _ := svc.Layer(context.Background(), "msk", "parks", nil)
	for _, f := range fc.Features {
		if f.Geometry.Type == "LineString" {
			t.Fatal("линии метро протекли в слой парков")
		}
	}
}
```

Имена фейков (`fakePOILister`, `fakeEvidenceLister`, `fakeListingLister`) и сигнатуру метода `Layer` взять из существующего файла теста — они там уже есть; если сигнатура отличается, использовать фактическую.

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `cd backend && go test ./internal/service/ -run Metro -v`
Expected: FAIL — `undefined: domain.MetroLine`

- [ ] **Step 3: Реализовать репозиторий**

Добавить в `backend/internal/domain/domain.go`:

```go
// MetroLine — линия рельсового транспорта для отрисовки на карте. System —
// enum, зафиксированный на трёх сторонах: subway / mck / mcd.
type MetroLine struct {
	Ref          string
	Name         string
	System       string
	Colour       string
	GeometryJSON string
}
```

Создать `backend/internal/repository/metro_repo.go`:

```go
// metro_repo.go — READ-ONLY доступ к Python-owned таблицам графа метро.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type MetroRepo struct {
	pool *pgxpool.Pool
}

func NewMetroRepo(pool *pgxpool.Pool) *MetroRepo {
	return &MetroRepo{pool: pool}
}

// ListLines returns rail lines with their drawing geometry for one city.
// Lines with no geometry are skipped: an empty LineString draws nothing and
// would only produce a broken feature on the map.
func (r *MetroRepo) ListLines(ctx context.Context, city string) ([]domain.MetroLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ml.ref, ml.name, ml.system, COALESCE(ml.colour, ''),
		       ST_AsGeoJSON(g.geom)
		FROM metro_line ml
		JOIN metro_line_geom g ON g.line_id = ml.id
		WHERE ml.city = $1 AND g.geom IS NOT NULL`, city)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.MetroLine
	for rows.Next() {
		var l domain.MetroLine
		if err := rows.Scan(&l.Ref, &l.Name, &l.System, &l.Colour, &l.GeometryJSON); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Подключить в geo_layers_service**

В `backend/internal/service/geo_layers_service.go` добавить интерфейс, поле и ветку:

```go
type metroLister interface {
	ListLines(ctx context.Context, city string) ([]domain.MetroLine, error)
}
```

Добавить `metro metroLister` в структуру `GeoLayersService` и параметр в `NewGeoLayersService`. В методе, собирающем слой, после сбора точек добавить:

```go
	// Слой metro — это не только точки станций: линии нужны, чтобы карта
	// показывала, куда эти станции ведут. Цвет и система едут в properties,
	// чтобы фронт не зашивал палитру у себя.
	if layer == "metro" && s.metro != nil {
		lines, err := s.metro.ListLines(ctx, city)
		if err != nil {
			return geojson.FeatureCollection{}, err
		}
		for _, l := range lines {
			fc.Features = append(fc.Features, geojson.RawFeature(l.GeometryJSON,
				map[string]any{"ref": l.Ref, "name": l.Name,
					"system": l.System, "colour": l.Colour}))
		}
	}
```

Обновить конструктор в `backend/internal/app/app.go`, где создаётся `GeoLayersService`, передав `repository.NewMetroRepo(pool)`.

- [ ] **Step 5: Запустить тесты**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/repository/metro_repo.go backend/internal/domain/domain.go backend/internal/service/geo_layers_service.go backend/internal/service/geo_layers_service_test.go backend/internal/app/app.go
git commit -m "feat: линии метро, МЦК и МЦД в слое карты"
```

---

# Этап E — фронт

## Task 16: Типы контракта на фронте

**Files:**
- Modify: `frontend/lib/agent/types.ts:116-128`
- Test: `frontend/test/fixtures.ts`

**Interfaces:**
- Consumes: JSON-форму из Задач 10 и 14.
- Produces: `MetroSystem`, `MetroSegment`, `MetroTransfer`, `MetroRide`, поле `RouteLeg.metro?: MetroRide`, фикстуру `metroRideFixture`.

- [ ] **Step 1: Написать типы и фикстуру**

В `frontend/lib/agent/types.ts` после `TravelMode` добавить:

```ts
/** Зафиксировано на трёх сторонах: habitus/online/schema.py ↔ Go ↔ здесь. */
export type MetroSystem = "subway" | "mck" | "mcd";

export interface MetroSegment {
  line_ref: string;
  line_name: string;
  system: MetroSystem;
  colour: string | null;
  from_station: string;
  to_station: string;
  stops: number;
  minutes: number;
  /** Время выведено из расстояния, а не взято из курируемого файла. */
  estimated: boolean;
}

export interface MetroTransfer {
  from_station: string;
  to_station: string;
  minutes: number;
  /** Переход улицей (типично метро↔МЦД) — рисуется отдельным пешим шагом. */
  outdoor: boolean;
  estimated: boolean;
}

/** Разбивка метро-ноги. Итог «от двери до двери» — в RouteLeg.minutes;
 *  здесь то, из чего он сложился. Складывать разбивку заново нельзя:
 *  округления разойдутся с итогом. */
export interface MetroRide {
  walk_from_home_min: number;
  walk_to_dest_min: number;
  segments: MetroSegment[];
  transfers: MetroTransfer[];
  total_minutes: number;
  estimated: boolean;
}
```

В `RouteLeg` добавить поле:

```ts
  /** Есть только у ног с mode === "metro". */
  metro?: MetroRide;
```

В `frontend/test/fixtures.ts` добавить:

```ts
import type { MetroRide } from "@/lib/agent/types";

export const metroRideFixture: MetroRide = {
  walk_from_home_min: 7,
  walk_to_dest_min: 5,
  total_minutes: 32,
  estimated: false,
  segments: [
    { line_ref: "1", line_name: "Сокольническая", system: "subway",
      colour: "#EF161E", from_station: "Сокольники", to_station: "Охотный Ряд",
      stops: 6, minutes: 13, estimated: false },
    { line_ref: "D1", line_name: "МЦД-1", system: "mcd", colour: "#F6A800",
      from_station: "Белорусская", to_station: "Одинцово",
      stops: 4, minutes: 18, estimated: true },
  ],
  transfers: [
    { from_station: "Охотный Ряд", to_station: "Белорусская",
      minutes: 8, outdoor: true, estimated: false },
  ],
};
```

- [ ] **Step 2: Проверить сборку типов**

Run: `cd frontend && npx tsc --noEmit`
Expected: без ошибок

- [ ] **Step 3: Commit**

```bash
git add frontend/lib/agent/types.ts frontend/test/fixtures.ts
git commit -m "feat: типы разбивки метро-поездки на фронте"
```

---

## Task 17: Лента-схема поездки

**Files:**
- Create: `frontend/components/passport/viz/MetroRouteStrip.tsx`
- Create: `frontend/components/passport/viz/MetroRouteStrip.test.tsx`
- Modify: `frontend/components/passport/viz/FamilyDayGraph.tsx`

**Interfaces:**
- Consumes: `MetroRide` (Задача 16).
- Produces: `export default function MetroRouteStrip({ ride }: { ride: MetroRide })`, `export const SYSTEM_LABEL: Record<MetroSystem, string>`.

- [ ] **Step 1: Написать падающий тест**

Создать `frontend/components/passport/viz/MetroRouteStrip.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import MetroRouteStrip from "./MetroRouteStrip";
import { metroRideFixture } from "@/test/fixtures";

describe("MetroRouteStrip", () => {
  it("показывает пешие плечи с обоих концов", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    expect(screen.getByText(/7 мин пешком/)).toBeInTheDocument();
    expect(screen.getByText(/5 мин пешком/)).toBeInTheDocument();
  });

  it("показывает станции и число перегонов каждого отрезка", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const first = screen.getByTestId("segment-0");
    expect(within(first).getByText(/Сокольники/)).toBeInTheDocument();
    expect(within(first).getByText(/Охотный Ряд/)).toBeInTheDocument();
    expect(within(first).getByText(/6 станций/)).toBeInTheDocument();
  });

  it("подписывает систему словом, а не только цветом", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    // цвет никогда не единственный носитель смысла
    expect(screen.getByTestId("segment-1")).toHaveTextContent("МЦД");
    expect(screen.getByTestId("segment-0")).toHaveTextContent("метро");
  });

  it("рисует уличную пересадку отдельным пешим шагом с её минутами", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const t = screen.getByTestId("transfer-0");
    expect(t).toHaveTextContent("8 мин");
    expect(t).toHaveTextContent(/улицей/i);
  });

  it("помечает оценочные отрезки словом", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    expect(screen.getByTestId("segment-1")).toHaveTextContent(/оценка/i);
    expect(screen.getByTestId("segment-0")).not.toHaveTextContent(/оценка/i);
  });

  it("показывает итог, а не сумму частей", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    // 7+13+8+18+5 = 51, но итог из контракта — 32; складывать заново нельзя
    expect(screen.getByTestId("total")).toHaveTextContent("32");
  });

  it("не падает на поездке без пересадок", () => {
    const ride = { ...metroRideFixture, transfers: [],
                   segments: [metroRideFixture.segments[0]] };
    render(<MetroRouteStrip ride={ride} />);
    expect(screen.queryByTestId("transfer-0")).toBeNull();
  });

  it("даёт маршруту доступную подпись целиком", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const list = screen.getByRole("list", { name: /маршрут/i });
    expect(list).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `cd frontend && npm test -- MetroRouteStrip`
Expected: FAIL — `Cannot find module './MetroRouteStrip'`

- [ ] **Step 3: Реализовать**

Создать `frontend/components/passport/viz/MetroRouteStrip.tsx`:

```tsx
"use client";
import type { MetroRide, MetroSystem } from "@/lib/agent/types";

// Подпись системы словом. Цвет линии подсказывает, текст утверждает — тем же
// правилом, что уже соблюдает FamilyDayGraph: цвет никогда не единственный
// носитель смысла.
export const SYSTEM_LABEL: Record<MetroSystem, string> = {
  subway: "метро",
  mck: "МЦК",
  mcd: "МЦД",
};

// Запасной цвет для линии, у которой в OSM не проставлен colour.
const FALLBACK_COLOUR = "#71717a";

function Dot({ colour }: { colour: string | null }) {
  return (
    <span
      aria-hidden
      className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
      style={{ background: colour || FALLBACK_COLOUR }}
    />
  );
}

function Walk({ minutes, note }: { minutes: number; note?: string }) {
  return (
    <li className="flex items-center gap-2 text-sm text-zinc-600">
      <span aria-hidden className="inline-block h-2.5 w-2.5 shrink-0 rounded-full border border-dashed border-zinc-400" />
      <span>
        {minutes} мин пешком{note ? ` ${note}` : ""}
      </span>
    </li>
  );
}

export default function MetroRouteStrip({ ride }: { ride: MetroRide }) {
  return (
    <div className="rounded-lg border border-zinc-100 p-3">
      <ol aria-label="маршрут поездки" className="flex flex-col gap-2">
        <Walk minutes={ride.walk_from_home_min} note="до станции" />

        {ride.segments.map((seg, i) => (
          <li key={`${seg.line_ref}-${i}`} data-testid={`segment-${i}`}
              className="flex items-start gap-2 text-sm">
            <span className="mt-1.5"><Dot colour={seg.colour} /></span>
            <span className="text-[#1c1d20]">
              <span className="font-medium">{seg.from_station}</span>
              {" → "}
              <span className="font-medium">{seg.to_station}</span>
              <span className="text-zinc-500">
                {" · "}{SYSTEM_LABEL[seg.system]}
                {seg.system === "subway" ? "" : `-${seg.line_ref}`}
                {" · "}{seg.stops} станций
                {" · "}{seg.minutes} мин
                {seg.estimated ? " · оценка" : ""}
              </span>
            </span>

            {/* Пересадка идёт ПОСЛЕ отрезка, который она закрывает */}
            {ride.transfers[i] ? null : null}
          </li>
        ))}

        {ride.transfers.map((t, i) => (
          <li key={`t-${i}`} data-testid={`transfer-${i}`}
              className="flex items-center gap-2 text-sm text-zinc-600">
            <span aria-hidden className="inline-block h-2.5 w-2.5 shrink-0 rotate-45 border border-zinc-400" />
            <span>
              переход {t.from_station} → {t.to_station}
              {" · "}{t.minutes} мин
              {t.outdoor ? " · улицей" : ""}
              {t.estimated ? " · оценка" : ""}
            </span>
          </li>
        ))}

        <Walk minutes={ride.walk_to_dest_min} note="от станции" />
      </ol>

      <p data-testid="total" className="mt-3 border-t border-zinc-100 pt-2 text-sm font-medium text-[#1c1d20]">
        {/* Итог берётся из контракта, а НЕ складывается из частей: округления
            каждого шага разошлись бы с числом, по которому фильтровался поиск. */}
        {ride.total_minutes} мин от двери до двери
        {ride.estimated ? (
          <span className="ml-2 font-normal text-zinc-500">
            часть перегонов оценена по расстоянию
          </span>
        ) : null}
      </p>
    </div>
  );
}
```

- [ ] **Step 4: Переставить пересадки в правильные места ленты**

Тест `рисует уличную пересадку отдельным пешим шагом` пройдёт и на текущем варианте, но лента при этом врёт порядком: все пересадки печатаются после всех отрезков. Заменить два отдельных `map` на один проход, чередующий отрезок и следующую за ним пересадку:

```tsx
        {ride.segments.map((seg, i) => (
          <Fragment key={`${seg.line_ref}-${i}`}>
            <li data-testid={`segment-${i}`} /* … как выше … */ />
            {ride.transfers[i] ? (
              <li data-testid={`transfer-${i}`} /* … как выше … */ />
            ) : null}
          </Fragment>
        ))}
```

Добавить `import { Fragment } from "react";` и убрать отдельный `map` по `transfers`. Добавить тест, фиксирующий порядок:

```tsx
  it("ставит пересадку между отрезками, которые она связывает", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const items = screen.getAllByRole("listitem");
    const ids = items.map((el) => el.getAttribute("data-testid"));
    expect(ids.indexOf("transfer-0")).toBeGreaterThan(ids.indexOf("segment-0"));
    expect(ids.indexOf("transfer-0")).toBeLessThan(ids.indexOf("segment-1"));
  });
```

- [ ] **Step 5: Подключить в FamilyDayGraph**

В `frontend/components/passport/viz/FamilyDayGraph.tsx` внутри отрисовки ноги (рядом со строкой 412, где печатается `leg.to_label`) добавить разворачивание ленты для метро-ног:

```tsx
{leg.mode === "metro" && leg.metro ? (
  <div className="mt-2">
    <MetroRouteStrip ride={leg.metro} />
  </div>
) : null}
```

и импорт `import MetroRouteStrip from "./MetroRouteStrip";`.

- [ ] **Step 6: Запустить тесты**

Run: `cd frontend && npm test -- MetroRouteStrip FamilyDayGraph`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add frontend/components/passport/viz/MetroRouteStrip.tsx frontend/components/passport/viz/MetroRouteStrip.test.tsx frontend/components/passport/viz/FamilyDayGraph.tsx
git commit -m "feat: лента-схема поездки на метро в паспорте объекта"
```

---

## Task 18: Линии метро на карте

**Files:**
- Modify: `frontend/lib/map/style.ts`
- Modify: `frontend/components/map/MapCanvas.tsx`
- Test: `frontend/components/map/MapCanvas.test.tsx`

**Interfaces:**
- Consumes: LineString-фичи слоя `metro` с `properties.system` и `properties.colour` (Задача 15).
- Produces: слой линий в стиле карты; изменений в публичных сигнатурах компонентов нет.

- [ ] **Step 1: Написать падающий тест**

Добавить в `frontend/components/map/MapCanvas.test.tsx`:

```tsx
it("рисует линии метро цветом из properties, а не зашитой палитрой", () => {
  const fc = {
    type: "FeatureCollection" as const,
    features: [{
      type: "Feature" as const,
      properties: { ref: "D1", name: "МЦД-1", system: "mcd", colour: "#F6A800" },
      geometry: { type: "LineString" as const,
                  coordinates: [[37.5, 55.7], [37.6, 55.8]] },
    }],
  };
  const layers = metroLineLayers(fc);
  expect(layers).toHaveLength(1);
  // цвет берётся из данных: палитра живёт на бэке, а не дублируется тут
  expect(layers[0].paint["line-color"]).toEqual(["get", "colour"]);
});

it("не роняет карту на линии без цвета", () => {
  const fc = {
    type: "FeatureCollection" as const,
    features: [{
      type: "Feature" as const,
      properties: { ref: "1", name: "л1", system: "subway", colour: "" },
      geometry: { type: "LineString" as const,
                  coordinates: [[37.5, 55.7], [37.6, 55.8]] },
    }],
  };
  expect(() => metroLineLayers(fc)).not.toThrow();
});
```

Добавить импорт `import { metroLineLayers } from "@/lib/map/style";`.

- [ ] **Step 2: Запустить и убедиться, что тест падает**

Run: `cd frontend && npm test -- MapCanvas`
Expected: FAIL — `metroLineLayers is not a function`

- [ ] **Step 3: Реализовать**

Добавить в `frontend/lib/map/style.ts`:

```ts
/** Запасной цвет для линии, у которой в OSM не проставлен colour. */
const METRO_FALLBACK_COLOUR = "#71717a";

/**
 * Слои для линий метро, МЦК и МЦД. Цвет берётся выражением ["get","colour"] из
 * properties фичи: палитра принадлежит данным и живёт на бэке — дублировать её
 * здесь значит завести второй источник правды, который разойдётся с первым.
 * МЦД рисуются пунктиром: у диаметров интервал в разы больше метро, и на карте
 * это стоит различать не только цветом.
 */
export function metroLineLayers(fc: GeoJSON.FeatureCollection) {
  if (!fc.features.length) return [];
  return [{
    id: "metro-lines",
    type: "line" as const,
    paint: {
      "line-color": ["get", "colour"],
      "line-width": 2.5,
      "line-opacity": 0.85,
    },
    layout: { "line-cap": "round" as const, "line-join": "round" as const },
  }];
}

export { METRO_FALLBACK_COLOUR };
```

В `frontend/components/map/MapCanvas.tsx` при получении слоя `metro` подставить запасной цвет для фич без него — иначе выражение `["get","colour"]` вернёт пустую строку и линия не отрисуется:

```tsx
// Пустой colour приходит от линий, у которых тега нет в OSM (см. протокол
// разведки). Подставляем запасной здесь, а не в выражении стиля: так в
// properties остаётся ровно то значение, которым линия будет нарисована.
const withColour = {
  ...fc,
  features: fc.features.map((f) => ({
    ...f,
    properties: { ...f.properties, colour: f.properties?.colour || METRO_FALLBACK_COLOUR },
  })),
};
```

- [ ] **Step 4: Запустить тесты**

Run: `cd frontend && npm test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/lib/map/style.ts frontend/components/map/MapCanvas.tsx frontend/components/map/MapCanvas.test.tsx
git commit -m "feat: линии метро, МЦК и МЦД на карте"
```

---

## Task 19: Прогон на живых данных и фиксация метрик

Спека требует не подкручивать пороги гейта, а перемерить и записать.

**Files:**
- Create: `docs/notes/metro-rollout-2026-08-29.md`

**Interfaces:**
- Consumes: всё предыдущее.
- Produces: заметку с замерами; при просадке порогов — новые значения `_DEFAULT_MIN_PRECISION` / `_DEFAULT_MIN_NDCG` в `habitus/cli.py` вместе с обоснованием.

- [ ] **Step 1: Собрать граф обоих городов**

```bash
docker compose up -d db
uv run habitus metro --city msk
uv run habitus metro --city spb
```

Ожидается непустая статистика по всем трём системам для Москвы и по `subway` для Петербурга; список `failed` пуст.

- [ ] **Step 2: Проверить связность и долю оценок**

```bash
docker compose exec -T db psql -U habitus -d habitus <<'SQL'
SELECT city, system, count(DISTINCT ml.id) AS lines, count(s.id) AS stations
FROM metro_line ml LEFT JOIN metro_station s ON s.line_id = ml.id
GROUP BY city, system ORDER BY city, system;

SELECT city, count(*) FILTER (WHERE estimated) AS estimated, count(*) AS total
FROM metro_edge GROUP BY city;

SELECT city, count(*) FILTER (WHERE outdoor) AS outdoor, count(*) AS total
FROM metro_transfer GROUP BY city;
SQL
```

- [ ] **Step 3: Прогнать метрики поиска**

```bash
uv run habitus eval
```

- [ ] **Step 4: Записать заметку**

Создать `docs/notes/metro-rollout-2026-08-29.md`: числа из шагов 2 и 3, доля оценочных перегонов по городам, значения precision@10 и NDCG@10 до и после, и вывод — держатся ли пороги `habitus/cli.py`.

Если пороги просели: **не подкручивать их молча.** Записать в заметку новые измеренные значения, причину просадки и только тогда править `_DEFAULT_MIN_PRECISION` / `_DEFAULT_MIN_NDCG`, сославшись на эту заметку в комментарии — ровно как требует комментарий у порогов в `habitus/cli.py:22`.

- [ ] **Step 5: Прогнать все три набора тестов**

```bash
uv run pytest
cd backend && go test ./... && cd ..
cd frontend && npm test && cd ..
```

- [ ] **Step 6: Commit**

```bash
git add docs/notes/metro-rollout-2026-08-29.md habitus/cli.py
git commit -m "docs: замеры графа метро и метрик поиска после раскатки"
```

---

## Самопроверка плана

**Покрытие спеки.** Каждый раздел спеки имеет задачу: §1 извлечение из OSM → Задачи 1–3; §2 схема данных → Задача 5; §3 курируемые времена → Задача 4; §4 движок → Задача 9; §5 пешие плечи → Задача 7 (включая `walk_min_metro` только по подземке); §6 контракт → Задача 10; §7 поиск → Задачи 12–13; §8 Go-шлюз → Задачи 14–15; §9 фронт → Задачи 16–18; §10 досье и города → Задача 11. Риск «порог eval может дрогнуть» → Задача 19.

**Два места, где план сознательно оставляет работу исполнителю, и это не заглушки:**

- **Задача 1** исследовательская по построению: спека прямо запрещает фиксировать теги МЦК и МЦД по памяти. Её выход — протокол и фикстура, значения `TRANSIT_RELATION_FILTER` в Задаче 3 сверяются с ним на шаге 4.
- **Задача 4, шаг 4** — наполнение курируемых файлов. Формат, ключи и пример заданы полностью; объём наполнения зависит от того, сколько линий вернула разведка. Пустые `edges` — рабочее стартовое состояние: каждый неописанный перегон получает помеченную оценку.

**Две шероховатости, снятые внутри задач.** В Задаче 9 первая версия `_segment` выбирает соседа неоднозначно на кольцевых линиях — шаг 4 требует протащить фактический срез пути. В Задаче 17 первая версия печатает все пересадки после всех отрезков — шаг 4 требует чередования и добавляет тест на порядок. Оба места помечены явно, чтобы исполнитель не принял первую версию за финальную.

**Согласованность имён.** `estimated` — одно и то же поле в БД, Python, Go и TypeScript. `system` принимает `subway`/`mck`/`mcd` во всех четырёх слоях. `total_minutes` в `MetroRide` равен `RouteLeg.minutes` — зафиксировано тестами в Задачах 10, 14 и 17. `walk_seconds` (секунды) живёт только в БД и Python; наружу везде едут минуты.
