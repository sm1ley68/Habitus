# Провенанс в досье — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Пользователь досье видит по каждому блоку, откуда взялись данные — наблюдение, вычисление или модельный прокси — и на какую дату они актуальны.

**Architecture:** Новое поле `sources: list[BlockSource]` у блока досье, сквозное на трёх сторонах контракта (Python → Go → TypeScript). Происхождение перестаёт быть прозой в `verdict_line` и счётчиком `layers_checked` и становится данными. Худший уровень нигде не хранится — выводится из списка при отрисовке.

**Tech Stack:** Python 3 / Pydantic / psycopg (ML-сервис), Go / Fiber (шлюз), Next.js / TypeScript / Vitest (фронт), PostgreSQL + PostGIS.

**Spec:** `docs/superpowers/specs/2026-09-03-dossier-provenance-design.md`

## Global Constraints

- Координаты везде `[lng, lat]`, WGS84 (EPSG:4326).
- Синтетическое значение вместо отсутствующего запрещено. Нет даты — `None`/`null`, **никогда** `now()`.
- Enum'ы зафиксированы на трёх сторонах: `habitus/online/schema.py` ↔ Go `internal/service/` ↔ `frontend/lib/agent/types.ts`. Меняются только вместе.
- Коммиты — Conventional Commits на русском (`feat:`, `fix:`, `test:`, `refactor:`). Без трейлеров и подписей.
- Работа идёт напрямую в `main`.
- Версия схемы досье остаётся `dossier-v1`: добавление необязательного поля контракт не ломает.
- Тесты с БД скипаются без поднятого Postgres, а не падают.
- Порядок строгости уровней: `proxy > computation > observation`.

---

### Task 1: Контракт `BlockSource` и честные даты

**Files:**
- Modify: `habitus/online/schema.py` (рядом с `LifestyleBlock`, строка 320)
- Modify: `habitus/online/dossier.py` (импорты и новые помощники)
- Test: `tests/test_dossier.py`

**Interfaces:**
- Produces: `BlockSource(key, label, kind, basis, observed_at)`, `SourceKind = Literal["observation","computation","proxy"]`, `LifestyleBlock.sources: list[BlockSource]`, `_table_updated_at(conn, table) -> date | None`, `_evidence_observed_at(conn, layer, lon, lat, city, radius_m=500) -> date | None`

- [ ] **Step 1: Написать падающие тесты**

В `tests/test_dossier.py` добавить импорт `from datetime import date` и `_evidence_observed_at`, `_table_updated_at` к существующему импорту из `habitus.online.dossier`, затем:

```python
def test_evidence_observed_at_returns_layer_date(dossier_conn):
    with dossier_conn.cursor() as cur:
        cur.execute("TRUNCATE urban_evidence;")
        cur.execute("""INSERT INTO urban_evidence
                           (source_id, source, city, layer, geom, db, observed_at)
                       VALUES ('n1','test','msk','noise',
                               ST_SetSRID(ST_MakePoint(37.60,55.75),4326),
                               55, '2026-05-01');""")
    dossier_conn.commit()
    assert _evidence_observed_at(
        dossier_conn, "noise", 37.60, 55.75, "msk") == date(2026, 5, 1)


def test_evidence_observed_at_is_none_when_nothing_in_radius(dossier_conn):
    """Пустой слой даёт None, а не сегодняшнюю дату. Подставленная дата —
    это синтетическое значение вместо отсутствующего замера."""
    with dossier_conn.cursor() as cur:
        cur.execute("TRUNCATE urban_evidence;")
    dossier_conn.commit()
    assert _evidence_observed_at(
        dossier_conn, "noise", 37.60, 55.75, "msk") is None


def test_table_updated_at_refuses_unknown_table(dossier_conn):
    """Имя таблицы подставляется в SQL строкой, поэтому список закрытый."""
    assert _table_updated_at(dossier_conn, "listings; DROP TABLE poi") is None
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_dossier.py -k "observed_at or updated_at" -v`
Expected: FAIL — `ImportError: cannot import name '_evidence_observed_at'`

- [ ] **Step 3: Добавить типы в `habitus/online/schema.py`**

К импортам добавить `from datetime import date`. Перед `class LifestyleBlock`:

```python
SourceKind = Literal["observation", "computation", "proxy"]


class BlockSource(BaseModel):
    """Происхождение одной величины блока.

    kind — способ получения величины, а не мера доверия к информанту:
    информант называется в basis. observed_at = None означает «дата
    неприменима»: величина считается на месте и не устаревает.
    """
    key: str
    label: str
    kind: SourceKind
    basis: str
    observed_at: date | None = None
```

В `LifestyleBlock` добавить поле последним:

```python
    sources: list[BlockSource] = []
```

- [ ] **Step 4: Добавить помощники в `habitus/online/dossier.py`**

К импортам добавить `from datetime import date` и `BlockSource` из `habitus.online.schema`. Рядом с `_fact_num`:

```python
# Таблицы, у которых мы спрашиваем дату импорта. Список закрытый: имя
# таблицы подставляется в SQL строкой, параметром его не передать.
_DATED_TABLES = {"poi", "urban_features", "metro_station"}


def _table_updated_at(conn, table: str) -> date | None:
    """Дата последнего импорта таблицы. None — таблица пуста или неизвестна."""
    if conn is None or table not in _DATED_TABLES:
        return None
    with conn.cursor() as cur:
        cur.execute(f"SELECT MAX(updated_at) FROM {table}")
        value = cur.fetchone()[0]
    return value.date() if value else None


def _evidence_observed_at(conn, layer: str, lon: float, lat: float,
                          city: str, radius_m: int = 500) -> date | None:
    """Дата ровно тех строк слоя, что попали в радиус и вошли в оценку, —
    а не всего слоя целиком."""
    if conn is None:
        return None
    with conn.cursor() as cur:
        cur.execute("""
            WITH home AS (SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom)
            SELECT MAX(e.observed_at) FROM urban_evidence e, home
            WHERE e.city = %s AND e.layer = %s
              AND ST_DWithin(e.geom::geography, home.geom::geography, %s)
        """, (lon, lat, city, layer, radius_m))
        value = cur.fetchone()[0]
    return value.date() if value else None
```

- [ ] **Step 5: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_dossier.py -v`
Expected: PASS, прежние тесты файла тоже зелёные.

- [ ] **Step 6: Коммит**

```bash
git add habitus/online/schema.py habitus/online/dossier.py tests/test_dossier.py
git commit -m "feat: контракт BlockSource и даты источников без подстановки now()"
```

---

### Task 2: Источники вторичных блоков

Вторичные блоки собираются из `listing.facts` без запросов в БД. Чтобы у них появилась дата импорта POI, `_secondary_blocks` начинает принимать соединение. Единственный вызывающий — `build_dossier` (`dossier.py:495`).

**Files:**
- Modify: `habitus/online/dossier.py:462-489` (`_secondary_blocks`), `:495` (вызов)
- Test: `tests/test_dossier.py`

**Interfaces:**
- Consumes: `BlockSource`, `_table_updated_at`, `_evidence_observed_at` из Task 1
- Produces: `_secondary_blocks(facts: dict, conn, city: str) -> list[LifestyleBlock]`

- [ ] **Step 1: Написать падающие тесты**

```python
def test_secondary_logistics_declares_computation_over_poi():
    blocks = _secondary_blocks({"walk_min_school": 8}, None, "msk")
    source = blocks[0].sources[0]
    assert source.kind == "computation"
    assert source.observed_at is None  # conn=None — дату спросить негде


def test_secondary_window_orientation_names_the_informant():
    """Сторону света извлекли из прозы объявления: наблюдение сделал
    продавец, и basis обязан это называть."""
    blocks = _secondary_blocks({"window_orientation": "S"}, None, "msk")
    source = next(s for b in blocks for s in b.sources if s.key == "window_orientation")
    assert source.kind == "observation"
    assert "продавц" in source.basis


def test_secondary_noise_is_proxy_not_computation():
    blocks = _secondary_blocks({"noise_level": "high"}, None, "msk")
    source = next(s for b in blocks for s in b.sources if s.key == "noise")
    assert source.kind == "proxy"
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_dossier.py -k secondary -v`
Expected: FAIL — `TypeError: _secondary_blocks() takes 1 positional argument but 3 were given`

- [ ] **Step 3: Переписать `_secondary_blocks`**

Заменить строки 462-489 на:

```python
def _secondary_blocks(facts: dict, conn, city: str) -> list[LifestyleBlock]:
    blocks = []
    poi_date = _table_updated_at(conn, "poi")
    school = _fact_num(facts, "walk_min_school")
    metro = _fact_num(facts, "walk_min_metro")
    if school is not None or metro is not None:
        value = school if school is not None else metro
        score = "A" if value <= 10 else "B+" if value <= 15 else "B" if value <= 20 else "C"
        blocks.append(LifestyleBlock(
            key="logistics", title="Логистика и школы", icon="school", score=score,
            verdict_line="Проверена пешая доступность.",
            description=f"Ближайшая подтверждённая точка — {value:g} мин пешком.",
            metrics={"minutes": value},
            sources=[BlockSource(
                key="poi_walk", label="Пешая доступность", kind="computation",
                basis="расчёт по POI OpenStreetMap", observed_at=poi_date)]))
    bars = _fact_num(facts, "bar_density_500m")
    if bars is not None:
        blocks.append(LifestyleBlock(
            key="social_environment", title="Окружение", icon="users",
            score="A" if bars == 0 else "B" if bars <= 2 else "C",
            verdict_line="Доступен подтверждённый слой заведений.",
            description=f"{bars:g} баров/алкомаркетов в радиусе 500 м.",
            metrics={"bars_500m": bars},
            sources=[BlockSource(
                key="bars", label="Заведения", kind="observation",
                basis="POI OpenStreetMap в радиусе 500 м", observed_at=poi_date)]))
    if facts.get("window_orientation") or facts.get("noise_level"):
        sources = []
        if facts.get("window_orientation"):
            sources.append(BlockSource(
                key="window_orientation", label="Сторона света", kind="observation",
                basis="указана продавцом в описании объявления"))
        if facts.get("noise_level"):
            sources.append(BlockSource(
                key="noise", label="Шум", kind="proxy",
                basis="модель по типам дорог"))
        blocks.append(LifestyleBlock(
            key="view_and_climate", title="Вид и климат", icon="sun", score="B",
            verdict_line="Часть климатических данных пока неполна.",
            description="Доступны только подтверждённые базовые характеристики окна и окружения.",
            sources=sources))
    return blocks
```

В `build_dossier` (строка 495) заменить вызов на:

```python
    blocks = _secondary_blocks(listing.facts, conn, req.city)
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_dossier.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/online/dossier.py tests/test_dossier.py
git commit -m "feat: источники вторичных блоков досье"
```

---

### Task 3: Источники блока «Социальное окружение»

**Files:**
- Modify: `habitus/online/dossier.py:531-541` (сборка hero-блока `social_environment`)
- Test: `tests/test_dossier.py`

**Interfaces:**
- Consumes: `BlockSource`, `_table_updated_at`, `_evidence_observed_at`

- [ ] **Step 1: Написать падающий тест**

```python
def test_social_block_marks_communal_and_crime_as_proxy(dossier_conn, monkeypatch):
    """Коммунальность и риск — модельные оценки. Пометить их вычислением
    значит выдать модель за замер."""
    from habitus.online import dossier as mod
    monkeypatch.setattr(mod, "_climate_data", lambda *a, **kw: None)
    monkeypatch.setattr(mod, "_family_data", lambda *a, **kw: None)
    with dossier_conn.cursor() as cur:
        cur.execute("TRUNCATE urban_evidence;")
        for layer, sid in (("communal", "c1"), ("crime", "k1")):
            cur.execute("""INSERT INTO urban_evidence
                               (source_id, source, city, layer, geom, weight, observed_at)
                           VALUES (%s,'test','msk',%s,
                                   ST_Buffer(ST_SetSRID(ST_MakePoint(37.60,55.75),4326)::geography,
                                             300)::geometry,
                                   0.4, '2026-04-10');""", (sid, layer))
    dossier_conn.commit()
    payload = build_dossier(DossierRequest(object_id="A", city="msk"), dossier_conn)
    block = next(b for b in payload.blocks if b.key == "social_environment")
    kinds = {s.key: s.kind for s in block.sources}
    assert kinds["communal"] == "proxy" and kinds["crime"] == "proxy"
    communal = next(s for s in block.sources if s.key == "communal")
    assert communal.observed_at == date(2026, 4, 10)
    # Проза больше не дублирует структуру: основание живёт в basis и только там.
    assert "по году постройки" in communal.basis
    assert "по году постройки" not in block.verdict_line
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `uv run pytest tests/test_dossier.py -k social_block -v`
Expected: FAIL — `KeyError: 'communal'`, список `sources` пуст.

- [ ] **Step 3: Заполнить `sources` у hero-блока**

В `build_dossier`, в ветке `if social:`, заменить конструктор блока на:

```python
        blocks.append(LifestyleBlock(
            key="social_environment", tier="hero", title="Социальное окружение",
            icon="users", score="A" if max(social.scores.model_dump().values()) < .34 else
            "B" if max(social.scores.model_dump().values()) < .67 else "C",
            verdict_line="Оценка окружения в радиусе 500 м.",
            description="Оценка в радиусе 500 м без подстановки отсутствующих данных.",
            data=social,
            sources=[
                BlockSource(key="communal", label="Коммунальность", kind="proxy",
                            basis="оценка по году постройки дома",
                            observed_at=_evidence_observed_at(
                                conn, "communal", listing.lon, listing.lat, req.city)),
                BlockSource(key="crime", label="Риск", kind="proxy",
                            basis="оценка по плотности алкомаркетов",
                            observed_at=_evidence_observed_at(
                                conn, "crime", listing.lon, listing.lat, req.city)),
                BlockSource(key="bars", label="Заведения", kind="observation",
                            basis="POI OpenStreetMap в радиусе 500 м",
                            observed_at=_table_updated_at(conn, "poi")),
            ]))
```

- [ ] **Step 4: Убедиться, что тест проходит**

Run: `uv run pytest tests/test_dossier.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/online/dossier.py tests/test_dossier.py
git commit -m "feat: источники блока социального окружения"
```

---

### Task 4: Источники блока «Вид и климат»

**Files:**
- Modify: `habitus/online/dossier.py:543-549` (сборка hero-блока `view_and_climate`)
- Test: `tests/test_dossier.py`

**Interfaces:**
- Consumes: `BlockSource`, `_table_updated_at`, `_evidence_observed_at`

- [ ] **Step 1: Написать падающий тест**

```python
def test_view_block_separates_computed_light_from_modelled_noise():
    """Блок смешивает разное по природе: инсоляция считается по геометрии,
    шум — модель. Одна пометка на весь блок была бы враньём."""
    from habitus.online.dossier import _view_climate_sources
    sources = {s.key: s.kind for s in _view_climate_sources(None, 37.6, 55.7, "msk")}
    assert sources["solar"] == "computation"
    assert sources["noise"] == "proxy"
    assert sources["cloudiness"] == "observation"
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `uv run pytest tests/test_dossier.py -k view_block -v`
Expected: FAIL — `ImportError: cannot import name '_view_climate_sources'`

- [ ] **Step 3: Вынести источники в функцию и подключить**

Рядом с `_evidence_observed_at` добавить:

```python
def _view_climate_sources(conn, lon: float, lat: float, city: str) -> list[BlockSource]:
    """Блок «Вид и климат» смешивает расчёт, модель и климатическую норму —
    поэтому источники перечисляются, а не сворачиваются в один."""
    return [
        BlockSource(key="solar", label="Инсоляция", kind="computation",
                    basis="расчёт по геометрии зданий",
                    observed_at=_table_updated_at(conn, "urban_features")),
        BlockSource(key="noise", label="Шум", kind="proxy",
                    basis="модель по типам дорог",
                    observed_at=_evidence_observed_at(conn, "noise", lon, lat, city)),
        BlockSource(key="cloudiness", label="Облачность", kind="observation",
                    basis="климатология NASA POWER"),
    ]
```

В `build_dossier`, в ветке `if climate:`, заменить конструктор блока на:

```python
        blocks.append(LifestyleBlock(
            key="view_and_climate", tier="hero", title="Вид и климат", icon="sun",
            score="A" if climate.db < 40 and climate.sun_hours_by_season.summer >= 5 else
            "B" if climate.db < 55 else "C",
            verdict_line="Освещённость, вид и шумовой фон.",
            description="Сезонная инсоляция, препятствия, тип вида и модельный шум.",
            data=climate,
            sources=_view_climate_sources(conn, listing.lon, listing.lat, req.city)))
```

- [ ] **Step 4: Убедиться, что тест проходит**

Run: `uv run pytest tests/test_dossier.py -v`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add habitus/online/dossier.py tests/test_dossier.py
git commit -m "feat: источники блока вида и климата"
```

---

### Task 5: Источники блока «Суточный ритм семьи» и чистка прозы

Здесь же из `verdict_line` всех трёх hero-блоков уходит происхождение: оно уже в структуре, а два представления одного факта разъедутся.

**Files:**
- Modify: `habitus/online/dossier.py:505-522` (сборка `family_routing`)
- Test: `tests/test_dossier.py`

**Interfaces:**
- Consumes: `BlockSource`, `_table_updated_at`

- [ ] **Step 1: Написать падающие тесты**

```python
def test_family_block_declares_graph_computation_without_date():
    """Маршрут считается на месте — датировать его нечем, и выдумывать
    дату нельзя."""
    from habitus.online.dossier import _family_sources
    sources = {s.key: s for s in _family_sources(None, has_metro=False)}
    assert sources["road_graph"].kind == "computation"
    assert sources["road_graph"].observed_at is None
    assert "metro_graph" not in sources


def test_family_block_adds_metro_source_only_when_metro_used():
    from habitus.online.dossier import _family_sources
    keys = {s.key for s in _family_sources(None, has_metro=True)}
    assert keys == {"road_graph", "metro_graph"}
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `uv run pytest tests/test_dossier.py -k "family_block or verdict_lines" -v`
Expected: FAIL — `ImportError: cannot import name '_family_sources'`

- [ ] **Step 3: Добавить функцию и упростить `verdict_line`**

Рядом с `_view_climate_sources` добавить:

```python
def _family_sources(conn, *, has_metro: bool) -> list[BlockSource]:
    sources = [BlockSource(key="road_graph", label="Дорожный граф",
                           kind="computation", basis="маршрут по дорожному графу")]
    if has_metro:
        sources.append(BlockSource(
            key="metro_graph", label="Граф метро/МЦК/МЦД", kind="computation",
            basis="перегоны, пересадки и интервалы",
            observed_at=_table_updated_at(conn, "metro_station")))
    return sources
```

В `build_dossier`, в ветке `if family:`, удалить весь блок вычисления `verdict_line` (строки с `has_road`, `if has_metro and has_road` и три ветки присваивания), оставив `has_metro`, и заменить конструктор блока на:

```python
        blocks.insert(0, LifestyleBlock(
            key="family_routing", tier="hero", title="Суточный ритм семьи",
            icon="route",
            score="A" if all(leg.safety == "safe" for m in family.members for leg in m.legs) else "B",
            verdict_line="Маршруты построены по подтверждённым данным.",
            description="Показаны только явно названные поездки и подтверждённые маршруты.",
            data=family,
            sources=_family_sources(conn, has_metro=has_metro)))
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `uv run pytest tests/test_dossier.py -v`
Expected: PASS

- [ ] **Step 5: Прогнать весь Python-набор**

Run: `uv run pytest`
Expected: PASS — падения в других файлах означают, что на старый `verdict_line` кто-то опирался; такой тест нужно поправить по смыслу, а не вернуть прозу.

- [ ] **Step 6: Коммит**

```bash
git add habitus/online/dossier.py tests/test_dossier.py
git commit -m "refactor: провенанс ушёл из verdict_line в структуру"
```

---

### Task 6: Go — `sources` переживает шлюз

Шлюз не проксирует досье насквозь: `decodeDossier` разбирает ответ ML в свою структуру, и фронту уходит именно она. Поля, которого нет в `Block`, фронт не увидит никогда.

**Files:**
- Modify: `backend/internal/service/object_service.go:94-104` (`Block`), `:106-152` (`UnmarshalJSON`)
- Test: `backend/internal/service/object_dossier_contract_test.go`

**Interfaces:**
- Consumes: JSON-контракт из Task 1-5
- Produces: `service.BlockSource`, `Block.Sources []BlockSource`

- [ ] **Step 1: Написать падающий тест**

```go
func TestDecodeDossierKeepsSourcesOnBlockWithoutData(t *testing.T) {
	// data:null — обычное состояние вторичного блока. У Block.UnmarshalJSON
	// на нём стоит ранний return, и присваивание Sources после него молча
	// теряло бы источники именно там, где они особенно нужны.
	var raw map[string]any
	_ = json.Unmarshal([]byte(`{
		"verdict":{"headline":"ok","confidence":0.5,"layers_checked":1},
		"brief":[],"compromises":[],"relaxation":[],"zone_rationale":"",
		"blocks":[{"key":"view_and_climate","title":"Вид и климат","score":"B",
		"description":"","data":null,"sources":[
		{"key":"noise","label":"Шум","kind":"proxy","basis":"модель по типам дорог",
		"observed_at":"2026-04-10"}]}]}`), &raw)
	dossier, ok := decodeDossier(raw)
	if !ok || len(dossier.Blocks) != 1 {
		t.Fatalf("decodeDossier() = %#v, %v", dossier, ok)
	}
	sources := dossier.Blocks[0].Sources
	if len(sources) != 1 || sources[0].Kind != "proxy" || sources[0].ObservedAt != "2026-04-10" {
		t.Fatalf("sources = %#v", sources)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `cd backend && go test ./internal/service/ -run TestDecodeDossierKeepsSources -v`
Expected: FAIL — `dossier.Blocks[0].Sources undefined`

- [ ] **Step 3: Добавить тип и поле**

Рядом с `type Block struct` добавить:

```go
// BlockSource — происхождение одной величины блока. Kind: observation |
// computation | proxy. ObservedAt пустой означает «дата неприменима»:
// величина считается на месте.
type BlockSource struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Basis      string `json:"basis"`
	ObservedAt string `json:"observed_at,omitempty"`
}
```

В `Block` добавить поле после `Metrics`:

```go
	Sources     []BlockSource  `json:"sources,omitempty"`
```

В `UnmarshalJSON` добавить в `raw` строку:

```go
		Sources     []BlockSource   `json:"sources"`
```

и присвоить **до раннего возврата по `data:null`** — сразу после `b.Metrics = raw.Metrics`:

```go
	b.Sources = raw.Sources
```

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 5: Коммит**

```bash
git add backend/internal/service/object_service.go backend/internal/service/object_dossier_contract_test.go
git commit -m "feat: шлюз проносит источники блоков досье до фронта"
```

---

### Task 7: Фронт — типы и компонент источников

**Files:**
- Modify: `frontend/lib/agent/types.ts:208-220` (`LifestyleBlock`)
- Create: `frontend/components/passport/dossier/BlockSources.tsx`
- Test: `frontend/components/passport/dossier/BlockSources.test.tsx`

**Interfaces:**
- Consumes: JSON-контракт из Task 6
- Produces: `worstKind(sources) -> SourceKind | null`, `<ProxyBadge sources={…} />`, `<BlockSources sources={…} />`

- [ ] **Step 1: Написать падающие тесты**

```tsx
import { render, screen } from "@testing-library/react";
import BlockSources, { ProxyBadge, worstKind } from "./BlockSources";
import type { BlockSource } from "@/lib/agent/types";

const proxy: BlockSource = {
  key: "noise", label: "Шум", kind: "proxy",
  basis: "модель по типам дорог", observed_at: "2026-04-10",
};
const computed: BlockSource = {
  key: "solar", label: "Инсоляция", kind: "computation",
  basis: "расчёт по геометрии зданий", observed_at: null,
};

test("худший уровень блока — прокси, даже если он один из трёх", () => {
  expect(worstKind([computed, proxy])).toBe("proxy");
});

test("плашка появляется только у блока с прокси", () => {
  render(<ProxyBadge sources={[computed, proxy]} />);
  expect(screen.getByText("оценка по модели")).toBeInTheDocument();
});

test("блок без прокси плашку не показывает — иначе помечено всё и не помечено ничто", () => {
  const { container } = render(<ProxyBadge sources={[computed]} />);
  expect(container).toBeEmptyDOMElement();
});

test("источник без даты рисуется без даты, а не с пустым местом", () => {
  render(<BlockSources sources={[computed]} />);
  expect(screen.getByText(/расчёт по геометрии зданий/)).toBeInTheDocument();
  expect(screen.queryByText("·", { exact: false })).not.toBeInTheDocument();
});

test("пустой список источников не рисует ничего", () => {
  const { container } = render(<BlockSources sources={[]} />);
  expect(container).toBeEmptyDOMElement();
});
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `cd frontend && npx vitest run components/passport/dossier/BlockSources.test.tsx`
Expected: FAIL — `Failed to resolve import "./BlockSources"`

- [ ] **Step 3: Добавить типы в `frontend/lib/agent/types.ts`**

Перед `export interface LifestyleBlock`:

```ts
/** Зафиксировано на трёх сторонах: habitus/online/schema.py ↔ Go ↔ здесь. */
export type SourceKind = "observation" | "computation" | "proxy";

export interface BlockSource {
  key: string;
  label: string;
  kind: SourceKind;
  basis: string;
  /** null/отсутствует — величина считается на месте, датировать нечем. */
  observed_at?: string | null;
}
```

В `LifestyleBlock` добавить поле:

```ts
  sources?: BlockSource[];
```

- [ ] **Step 4: Написать компонент**

```tsx
"use client";
import type { BlockSource, SourceKind } from "@/lib/agent/types";

const KIND_LABEL: Record<SourceKind, string> = {
  observation: "наблюдение",
  computation: "вычисление",
  proxy: "оценка по модели",
};

// Строгость по возрастанию. Плашку заслуживает только прокси: вычисление —
// нормальный режим работы продукта, и значок на нём обесценил бы значок на
// модели. Если помечено всё, не помечено ничто.
const SEVERITY: Record<SourceKind, number> = {
  observation: 0, computation: 1, proxy: 2,
};

export function worstKind(sources: BlockSource[]): SourceKind | null {
  if (!sources.length) return null;
  return sources.reduce<SourceKind>(
    (worst, s) => (SEVERITY[s.kind] > SEVERITY[worst] ? s.kind : worst),
    "observation",
  );
}

function when(iso?: string | null): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? null
    : d.toLocaleDateString("ru-RU", { month: "short", year: "numeric" });
}

export function ProxyBadge({ sources }: { sources?: BlockSource[] }) {
  if (worstKind(sources ?? []) !== "proxy") return null;
  return (
    <span className="rounded-full bg-[#f8f0e0] px-2 py-0.5 text-[11px] text-[#b3822f]">
      оценка по модели
    </span>
  );
}

export default function BlockSources({ sources }: { sources?: BlockSource[] }) {
  if (!sources?.length) return null;
  return (
    <ul className="mt-4 flex flex-col gap-1.5 border-t border-zinc-100 pt-3">
      {sources.map((s) => {
        const date = when(s.observed_at);
        return (
          <li key={s.key} className="text-xs leading-relaxed text-zinc-400">
            <span className="text-zinc-600">{s.label}</span> — {KIND_LABEL[s.kind]},{" "}
            {s.basis}
            {date && <span> · {date}</span>}
          </li>
        );
      })}
    </ul>
  );
}
```

- [ ] **Step 5: Убедиться, что тесты проходят**

Run: `cd frontend && npx vitest run components/passport/dossier/BlockSources.test.tsx && npx tsc --noEmit`
Expected: PASS, `tsc` без ошибок.

- [ ] **Step 6: Коммит**

```bash
git add frontend/lib/agent/types.ts frontend/components/passport/dossier/BlockSources.tsx frontend/components/passport/dossier/BlockSources.test.tsx
git commit -m "feat: компонент источников блока досье"
```

---

### Task 8: Фронт — встроить источники в оба рендерера блоков

Hero-блоки рисует `Chapter.tsx`, вторичные — `SecondaryGrid.tsx`. Провенанс нужен в обоих, иначе половина досье останется неразмеченной.

**Files:**
- Modify: `frontend/components/passport/dossier/Chapter.tsx`, `frontend/components/passport/dossier/SecondaryGrid.tsx`
- Test: `frontend/components/passport/dossier/BlockSources.test.tsx` (дописать)

**Interfaces:**
- Consumes: `BlockSources`, `ProxyBadge` из Task 7

- [ ] **Step 1: Написать падающие тесты**

Дописать в `BlockSources.test.tsx`:

```tsx
import Chapter from "./Chapter";
import SecondaryGrid from "./SecondaryGrid";
import type { LifestyleBlock } from "@/lib/agent/types";

const block: LifestyleBlock = {
  key: "view_and_climate", title: "Вид и климат", icon: "sun", score: "B",
  description: "Описание", sources: [proxy],
};

test("глава hero-блока показывает источники и плашку", () => {
  render(<Chapter block={block} index={0} />);
  expect(screen.getByText("оценка по модели")).toBeInTheDocument();
  expect(screen.getByText(/модель по типам дорог/)).toBeInTheDocument();
});

test("карточка вторичного блока тоже показывает источники", () => {
  render(<SecondaryGrid blocks={[block]} />);
  expect(screen.getByText(/модель по типам дорог/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Убедиться, что тесты падают**

Run: `cd frontend && npx vitest run components/passport/dossier/BlockSources.test.tsx`
Expected: FAIL — `Unable to find an element with the text: оценка по модели`

- [ ] **Step 3: Встроить в `Chapter.tsx`**

Добавить импорт `import BlockSources, { ProxyBadge } from "./BlockSources";`.

В строке заголовка, рядом с `<GradeBadge score={block.score} />`, обернуть оба в контейнер:

```tsx
            <div className="flex shrink-0 items-center gap-2">
              <ProxyBadge sources={block.sources} />
              <GradeBadge score={block.score} />
            </div>
```

В конце колонки-улики, после блока `{metrics.length > 0 && (…)}`, добавить:

```tsx
          <BlockSources sources={block.sources} />
```

- [ ] **Step 4: Встроить в `SecondaryGrid.tsx`**

Добавить тот же импорт. Рядом с `<GradeBadge score={block.score} />` в карточке:

```tsx
                  <div className="flex shrink-0 items-center gap-2">
                    <ProxyBadge sources={block.sources} />
                    <GradeBadge score={block.score} />
                  </div>
```

После абзаца с `{block.description}` добавить:

```tsx
                <BlockSources sources={block.sources} />
```

- [ ] **Step 5: Прогнать весь фронт**

Run: `cd frontend && npx vitest run && npx tsc --noEmit`
Expected: PASS — все файлы зелёные, `tsc` без ошибок.

- [ ] **Step 6: Коммит**

```bash
git add frontend/components/passport/dossier/
git commit -m "feat: источники данных видны в главах и карточках досье"
```

---

### Task 9: Сквозная проверка и документ

**Files:**
- Modify: `frontend/Пайплайн фронт.md` (раздел досье), `README.md` при необходимости
- Test: прогон всех трёх наборов

- [ ] **Step 1: Прогнать все три набора**

```bash
uv run pytest
cd backend && go test ./... && cd ..
cd frontend && npx vitest run && npx tsc --noEmit && cd ..
```
Expected: всё зелёное.

- [ ] **Step 2: Дописать контракт в `frontend/Пайплайн фронт.md`**

В описание объекта блока досье добавить поле `sources` с примером:

```json
"sources": [
  {"key": "noise", "label": "Шум", "kind": "proxy",
   "basis": "модель по типам дорог", "observed_at": "2026-04-10"}
]
```

и строку: «`kind` — способ получения величины: `observation` (наблюдение), `computation` (вычисление), `proxy` (модельная оценка). `observed_at` отсутствует, когда величина считается на месте; подставлять текущую дату запрещено.»

- [ ] **Step 3: Коммит**

```bash
git add frontend/Пайплайн\ фронт.md
git commit -m "docs: контракт источников данных в блоке досье"
```

---

## Самопроверка плана

**Покрытие спеки.** Контракт на трёх сторонах — Task 1 (Python), 6 (Go), 7 (TS). Карта источников из десяти строк — Task 2 (logistics, bars, сторона света, шум вторичного блока), 3 (коммунальность, риск, заведения), 4 (инсоляция, шум, облачность), 5 (дорожный граф, граф метро). Правило «худший уровень не хранится» — Task 7, `worstKind`. Чистка `verdict_line` — Task 5. Правило плашки только для прокси — Task 7. Запрет на `now()` вместо даты — Task 1, тест `test_evidence_observed_at_is_none_when_nothing_in_radius`. Совместимость со старым кэшем — поле необязательное на всех трёх сторонах, версия схемы не поднимается.

**Плейсхолдеров нет.** Каждый шаг с кодом содержит код целиком.

**Согласованность имён.** `BlockSource` (Python/Go/TS), `SourceKind`, поля `key/label/kind/basis/observed_at`, помощники `_table_updated_at`, `_evidence_observed_at`, `_view_climate_sources`, `_family_sources`, экспорт `worstKind`/`ProxyBadge`/`BlockSources` — одинаковы во всех задачах, где упоминаются.
