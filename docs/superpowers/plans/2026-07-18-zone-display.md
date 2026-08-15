# Явный показ зоны на фронте — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Показать распознанную область поиска явно — текстовый чип («Центр · ЦАО», «Хамовники») над выдачей и реальную границу зоны на карте вместо convex-hull.

**Architecture:** `resolve_area` — единый источник и фильтра, и отрисовки: `AreaMatch` несёт декларативный `geom_sql`/`geom_params`, хелпер `area_geojson()` собирает компактный FeatureCollection границы зоны. Пайплайн кладёт `area_label`+`area_geojson` в `SearchResponse`; Go пробрасывает их в SSE `final_result` (geojson → в `suggested_areas_geojson` вместо hull); фронт рисует чип, карта уже рендерит `zoneGeoJSON`.

**Tech Stack:** Python 3.12/uv, PostGIS, psycopg3, pytest; Go/Fiber; Next.js/TypeScript/MapLibre GL, vitest.

**Спека:** `docs/superpowers/specs/2026-07-18-zone-display-design.md` — источник правды.

## Global Constraints

- Python: `uv run pytest`. Dev-БД `postgresql://habitus:habitus@localhost:5544/habitus`. ⚠️ Тесты с БД гонять ТОЛЬКО на `habitus_test` (`DB_DSN="…/habitus_test" uv run pytest`), не на наполненной dev-БД.
- Go: `cd backend && go test ./...`. Frontend: `cd frontend && npm test`.
- Коммиты: Conventional Commits на русском, БЕЗ упоминания Claude/AI, БЕЗ трейлеров.
- Координаты везде `[lng, lat]`, WGS84. Enum'ы/контракт: `habitus/online/schema.py` ↔ Go `internal/client/ml_client.go` ↔ `frontend/lib/agent/types.ts`.
- Геометрия зоны НИКОГДА не роняет поиск: сбор `area_geojson` в пайплайне — в try/except, после retrieval.
- SQL-фрагменты `geom_sql` — литералы в коде; значения только через bind-параметры (`%s`).
- named-зоны/точка → круг `ST_Buffer`; округа́ → `ST_Union` полигонов; район/кольцо/имя-округа → полигон из `admin_zones`. Нет геометрии (`geom_sql=""`) → `area_geojson=None`, чип всё равно показывается.

---

### Task 1: ML — геометрия зоны в geo.py

**Files:**
- Modify: `habitus/online/geo.py`
- Test: `tests/test_area.py`

**Interfaces:**
- Produces (на них опирается Task 2):
  - `AreaMatch` с новыми полями `geom_sql: str = ""`, `geom_params: tuple = ()`.
  - `area_geojson(am: AreaMatch | None, conn) -> dict | None` — FeatureCollection границы зоны (одна Feature, `properties.label`, geometry Polygon/MultiPolygon), упрощённый; `None` если `geom_sql` пуст или геометрия NULL (зоны не импортированы).

- [ ] **Step 1: Падающие тесты** — в `tests/test_area.py` (использует существующий `_seeded_conn` из Task 5 гео-зон; он импортирует фикстуру округов ЦАО/САО + район Хамовники + named-сид):

```python
from habitus.online.geo import area_geojson


def test_cardinal_match_sets_geom_sql():
    m = resolve_area("центр")
    assert "ST_Union" in m.geom_sql and m.geom_params == (["ЦАО"],)


def test_area_geojson_okrug_returns_featurecollection():
    with _seeded_conn() as conn:
        m = resolve_area("центр")            # округ ЦАО (кардинал)
        fc = area_geojson(m, conn)
        assert fc["type"] == "FeatureCollection"
        geom = fc["features"][0]["geometry"]
        assert geom["type"] in ("Polygon", "MultiPolygon")
        assert fc["features"][0]["properties"]["label"] == m.label


def test_area_geojson_raion_and_named(_seeded=None):
    with _seeded_conn() as conn:
        assert area_geojson(resolve_area("Хамовники", conn), conn)["type"] == "FeatureCollection"
        assert area_geojson(resolve_area("Патрики", conn), conn)["type"] == "FeatureCollection"


def test_area_geojson_none_when_no_geom_or_empty_zones():
    with _seeded_conn() as conn:
        assert area_geojson(None, conn) is None
        with conn.cursor() as cur:
            cur.execute("TRUNCATE admin_zones;")
        conn.commit()
        # округа больше нет в БД → ST_Union NULL → None (чип уцелеет, карта откатится)
        assert area_geojson(resolve_area("центр"), conn) is None
```

- [ ] **Step 2: Запустить — FAIL**

Run: `DB_DSN="postgresql://habitus:habitus@localhost:5544/habitus_test" uv run pytest tests/test_area.py -k "geom or geojson" -v`
Expected: FAIL (нет полей `geom_sql`/`area_geojson`).

- [ ] **Step 3: AreaMatch += поля геометрии** — в `habitus/online/geo.py` заменить датакласс:

```python
@dataclass
class AreaMatch:
    sql: str
    params: tuple
    label: str
    widen: list  # list[tuple[str, tuple, str]] — шире→шире, финал ("TRUE", (), «вся Москва»)
    geom_sql: str = ""       # скалярное SQL-выражение геометрии зоны (для отрисовки)
    geom_params: tuple = ()  # bind-параметры к geom_sql
```

- [ ] **Step 4: Проставить geom_sql в ветках resolve_area** (`habitus/online/geo.py`):

`_okrug_match` (покрывает кардинал/центр/диагональ/за-МКАД — округа́ через union):
```python
def _okrug_match(okrugs: tuple[str, ...], label: str) -> AreaMatch:
    return AreaMatch(
        "okrug = ANY(%s)", (list(okrugs),), label, [_DROP],
        geom_sql="(SELECT ST_Union(geom) FROM admin_zones WHERE kind='okrug' AND name = ANY(%s))",
        geom_params=(list(okrugs),))
```

named-ветка (круг `ST_Buffer` вокруг якоря) — в конструкции `AreaMatch` для named добавить:
```python
        return AreaMatch(
            "ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
            (lon, lat, radius), name,
            [("ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
              (lon, lat, radius * 2), f"{name} (шире)"), _DROP],
            geom_sql="ST_Buffer(ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)::geometry",
            geom_params=(lon, lat, radius))
```

кольцо-ветка (полигон кольца из admin_zones):
```python
            return AreaMatch(
                "ST_Within(geom, (SELECT geom FROM admin_zones WHERE kind='ring' AND name=%s))",
                (rz[0],), rz[0], [("okrug = %s", ("ЦАО",), "ЦАО"), _DROP],
                geom_sql="(SELECT geom FROM admin_zones WHERE kind='ring' AND name=%s)",
                geom_params=(rz[0],))
```

имя района/округа-ветка (полигон из admin_zones по kind,name):
```python
    if zr:
        kind, name, parent = zr
        if kind == "raion":
            widen = []
            if parent:
                widen.append(("okrug = %s", (parent,), f"округ {parent}"))
            widen.append(_DROP)
            return AreaMatch("raion = %s", (name,), name, widen,
                             geom_sql="(SELECT geom FROM admin_zones WHERE kind='raion' AND name=%s)",
                             geom_params=(name,))
        return AreaMatch("okrug = %s", (name,), name, [_DROP],
                         geom_sql="(SELECT geom FROM admin_zones WHERE kind='okrug' AND name=%s)",
                         geom_params=(name,))
```

fallback-полигон (сам полигон места):
```python
        return AreaMatch(
            "ST_Within(geom, ST_SetSRID(ST_GeomFromGeoJSON(%s),4326))",
            (json.dumps(poly["geometry"]),), area,
            [("ST_DWithin(geom::geography, ST_Centroid(ST_SetSRID(ST_GeomFromGeoJSON(%s),4326))::geography, %s)",
              (json.dumps(poly["geometry"]), 5000.0), f"{area} (окрестность)"), _DROP],
            geom_sql="ST_SetSRID(ST_GeomFromGeoJSON(%s),4326)",
            geom_params=(json.dumps(poly["geometry"]),))
```

fallback-точка (круг 3км):
```python
        return AreaMatch(
            "ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
            (lon, lat, 3000.0), area,
            [("ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
              (lon, lat, 6000.0), f"{area} (шире)"), _DROP],
            geom_sql="ST_Buffer(ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)::geometry",
            geom_params=(lon, lat, 3000.0))
```

- [ ] **Step 5: Хелпер `area_geojson`** — добавить в `habitus/online/geo.py` (рядом с resolve_area):

```python
def area_geojson(am: "AreaMatch | None", conn) -> dict | None:
    """FeatureCollection границы зоны для карты (упрощённый) или None.
    None, если у зоны нет геометрии (geom_sql пуст) или полигон не собрался
    (зоны не импортированы → ST_Union NULL) — чип уцелеет, карта откатится к hull."""
    if am is None or not am.geom_sql or conn is None:
        return None
    row = conn.execute(
        f"SELECT ST_AsGeoJSON(ST_SimplifyPreserveTopology({am.geom_sql}, 0.0005), 5)",
        am.geom_params).fetchone()
    if not row or not row[0]:
        return None
    return {"type": "FeatureCollection", "features": [{
        "type": "Feature", "properties": {"label": am.label},
        "geometry": json.loads(row[0])}]}
```

- [ ] **Step 6: Запустить — PASS**

Run: `DB_DSN="…/habitus_test" uv run pytest tests/test_area.py -v`
Expected: PASS (новые + все прежние тесты area зелёные).

- [ ] **Step 7: Commit**

```bash
git add habitus/online/geo.py tests/test_area.py
git commit -m "feat: геометрия зоны в AreaMatch + хелпер area_geojson для отрисовки"
```

---

### Task 2: ML — проброс area_label/area_geojson в SearchResponse

**Files:**
- Modify: `habitus/online/schema.py`
- Modify: `habitus/online/pipeline.py`
- Test: `tests/test_pipeline.py`

**Interfaces:**
- Consumes: `resolve_area`, `area_geojson` (Task 1).
- Produces (на них опирается Task 3): `SearchResponse.area_label: str | None`, `SearchResponse.area_geojson: dict | None`.

- [ ] **Step 1: Падающий тест** — в `tests/test_pipeline.py` (рядом с `test_area_center_filters`, та же fixture `conn`):

```python
def test_area_label_and_geojson_surface(conn):
    with conn.cursor() as cur:
        cur.execute("UPDATE listings SET okrug='ЦАО' WHERE external_id='A';")
        cur.execute("UPDATE listings SET okrug='САО' WHERE external_id='B';")
    conn.commit()
    llm = FakeLLM([LLMResponse(content=None, tool_arguments=json.dumps(
        {"rooms": [2], "area": "центр", "semantic_text": "тихо"})), _explain_resp()])
    resp = run_search("двушка в центре", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker(), min_results=1)
    assert resp.area_label and "ЦАО" in resp.area_label
    # admin_zones в fixture пуст → геометрия не собирается, но контракт-поле присутствует
    assert resp.area_geojson is None or resp.area_geojson["type"] == "FeatureCollection"


def test_no_area_no_label(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp()])   # без area
    resp = run_search("тихая двушка", conn, llm=llm, model=FakeModel(), reranker=FakeReranker())
    assert resp.area_label is None and resp.area_geojson is None
```

- [ ] **Step 2: Запустить — FAIL**

Run: `DB_DSN="…/habitus_test" uv run pytest tests/test_pipeline.py -k "area_label or no_area" -v`
Expected: FAIL (`SearchResponse` не имеет `area_label`).

- [ ] **Step 3: schema.py** — в `SearchResponse` (`habitus/online/schema.py`) добавить два поля:

```python
class SearchResponse(BaseModel):
    results: list[ResultItem]
    explanation: str             # только поверх фактов из БД
    parsed: ParsedQuery          # что поняли (прозрачность/дебаг)
    relaxed: list[str] = []      # какие ограничения ослаблены relaxation-петлёй
    data_freshness: str          # «данные актуальны на …» (max updated_at)
    degraded: list[str] = []     # какие слои отвалились
    area_label: str | None = None    # человекочитаемая зона: «центр (ЦАО)», «Хамовники»
    area_geojson: dict | None = None  # FeatureCollection границы зоны для карты
```

- [ ] **Step 4: pipeline.py** — в `habitus/online/pipeline.py`.

(4a) В блоке резолва области (шаг «2.5») сохранить лейбл рядом с `area_match`:
```python
    from habitus.online.geo import resolve_area
    area_match = None
    if pq.area:
        try:
            with trace.span("resolve_area"):
                area_match = resolve_area(pq.area, conn)
        except Exception as exc:
            log.warning("резолв области не удался: %s", exc, exc_info=True)
    area_label = area_match.label if area_match else None
```

(4b) Перед сборкой `SearchResponse` (после rerank, рядом с `data_freshness`) собрать геометрию защищённо и вернуть новые поля:
```python
    area_geo = None
    if area_match is not None:
        try:
            from habitus.online.geo import area_geojson
            area_geo = area_geojson(area_match, conn)
        except Exception as exc:
            log.warning("сбор геометрии зоны не удался: %s", exc, exc_info=True)

    return SearchResponse(results=results, explanation=explanation, parsed=pq,
                          relaxed=relaxed, data_freshness=data_freshness,
                          degraded=degraded, area_label=area_label,
                          area_geojson=area_geo)
```

- [ ] **Step 5: Запустить — PASS**

Run: `DB_DSN="…/habitus_test" uv run pytest tests/test_pipeline.py -v`
Expected: PASS (новые + прежние pipeline-тесты зелёные).

- [ ] **Step 6: Полный сьют**

Run: `DB_DSN="…/habitus_test" uv run pytest -q`
Expected: всё зелёное.

- [ ] **Step 7: Commit**

```bash
git add habitus/online/schema.py habitus/online/pipeline.py tests/test_pipeline.py
git commit -m "feat: area_label и area_geojson в ответе поиска"
```

---

### Task 3: Go — проброс area_label/area_geojson в SSE

**Files:**
- Modify: `backend/internal/client/ml_client.go`
- Modify: `backend/internal/service/search_stream_service.go`
- Test: `backend/internal/service/search_stream_service_test.go` (создать при отсутствии)

**Interfaces:**
- Consumes: ML `SearchResponse.area_label`/`area_geojson` (Task 2).
- Produces (на них опирается Task 4): SSE-событие `final_result` с полями `area_label` и `suggested_areas_geojson` (= граница зоны при наличии, иначе прежний hull).

- [ ] **Step 1: DTO** — в `backend/internal/client/ml_client.go` в структуру `SearchResponse` добавить два поля (рядом с `Degraded`):

```go
	AreaLabel   string `json:"area_label"`
	AreaGeojson any    `json:"area_geojson"`
```

- [ ] **Step 2: FinalResultEvent += AreaLabel** — в `backend/internal/service/search_stream_service.go`:

```go
type FinalResultEvent struct {
	SuggestedAreasGeoJSON any                 `json:"suggested_areas_geojson"`
	Objects               []FinalResultObject `json:"objects"`
	DataFreshness         string              `json:"data_freshness"`
	AreaLabel             string              `json:"area_label"`
}
```

- [ ] **Step 3: Чистый выбор источника + buildFinalResult** — в `backend/internal/service/search_stream_service.go`.

(3a) Добавить чистую функцию (её и тестируем — вся логика «зона вместо hull» здесь):
```go
// pickSuggestedAreas: реальная граница зоны (из ML) заменяет convex-hull результатов.
func pickSuggestedAreas(hull, zone any) any {
	if zone != nil {
		return zone
	}
	return hull
}
```

(3b) В конце `buildFinalResult` заменить `areas := BuildSuggestedAreas(coords, customPoint)` и `return`:
```go
	suggested := pickSuggestedAreas(BuildSuggestedAreas(coords, customPoint), resp.AreaGeojson)

	objectIDs := make([]string, len(objects))
	for i, o := range objects {
		objectIDs[i] = o.ID
	}

	return FinalResultEvent{
		SuggestedAreasGeoJSON: suggested,
		Objects:               objects,
		DataFreshness:         resp.DataFreshness,
		AreaLabel:             resp.AreaLabel,
	}, objectIDs
```

- [ ] **Step 4: Тест чистой функции** — `backend/internal/service/search_stream_service_test.go` (создать/дополнить):

```go
package service

import "testing"

func TestPickSuggestedAreas(t *testing.T) {
	hull := map[string]any{"type": "FeatureCollection", "features": []any{"hull"}}
	zone := map[string]any{"type": "FeatureCollection", "features": []any{"zone"}}

	// зона есть → она вытесняет hull
	if got := pickSuggestedAreas(hull, zone); got == nil ||
		got.(map[string]any)["features"].([]any)[0] != "zone" {
		t.Fatalf("зона должна заменить hull, получили %v", got)
	}
	// зоны нет → остаётся hull
	if got := pickSuggestedAreas(hull, nil); got.(map[string]any)["features"].([]any)[0] != "hull" {
		t.Fatalf("без зоны должен остаться hull, получили %v", got)
	}
}
```

- [ ] **Step 5: Запустить**

Run: `cd backend && go build ./... && go test ./internal/service/ -run PickSuggestedAreas -v`
Expected: PASS (обе ветки — зона вытесняет hull, без зоны hull остаётся).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/client/ml_client.go backend/internal/service/search_stream_service.go backend/internal/service/search_stream_service_test.go
git commit -m "feat: проброс area_label и границы зоны в SSE final_result"
```

---

### Task 4: Фронт — чип зоны + тип геометрии

**Files:**
- Modify: `frontend/lib/agent/types.ts`
- Modify: `frontend/lib/api/searchStream.ts`
- Modify: `frontend/lib/store/session.ts`
- Create: `frontend/components/chat/ZoneChip.tsx`
- Modify: `frontend/components/result/ResultScreen.tsx` — вставить `<ZoneChip />` над гридом карта+список
- Test: `frontend/components/chat/ZoneChip.test.tsx`

Тест-раннер фронта — **vitest** + `@testing-library/react` + `@testing-library/jest-dom` (проверено); импорты в тесте ниже совместимы.

**Interfaces:**
- Consumes: SSE `final_result.area_label` + `suggested_areas_geojson` (Task 3).
- Produces: чип с зоной в результатном экране; карта рисует границу зоны (слой без изменений).

- [ ] **Step 1: Типы** — в `frontend/lib/agent/types.ts`:

(1a) Расширить `GeoZone.geometry` на MultiPolygon (union округов — MultiPolygon):
```typescript
    geometry: {
      type: "Polygon" | "MultiPolygon";
      coordinates: number[][][] | number[][][][];
    };
```
(1b) В типе результата рана (`RunResult`/payload `onDone`, где есть `zoneGeoJSON`, `chatId`) добавить `areaLabel: string | null`.

- [ ] **Step 2: searchStream — читать area_label** — в `frontend/lib/api/searchStream.ts`:

Добавить локальную переменную и чтение из `final_result`, прокинуть в `onDone`:
```typescript
          let zoneGeoJSON: GeoZone | null = null;
          let areaLabel: string | null = null;
          // ...
            } else if (f.event === "final_result") {
              properties = (f.data.objects as Property[]) ?? [];
              zoneGeoJSON = (f.data.suggested_areas_geojson as GeoZone) ?? null;
              areaLabel = (f.data.area_label as string) ?? null;
            }
          // ...
          if (!failed) handlers.onDone({ properties, zoneGeoJSON, areaLabel, chatId: chat.chat_id });
```

- [ ] **Step 3: store — хранить areaLabel** — в `frontend/lib/store/session.ts`:

Добавить в state `areaLabel: string | null` (инициализация `null`), а в `finish` проставлять:
```typescript
  areaLabel: null as string | null,
  // ...
  finish: ({ properties, zoneGeoJSON, areaLabel, chatId }) =>
    set({ properties, stage: "done", screen: "result", zoneGeoJSON, areaLabel, chatId }),
```

- [ ] **Step 4: Падающий тест** — `frontend/components/chat/ZoneChip.test.tsx` (повторить импорты/setup существующих фронт-тестов — см. `frontend/components/chat/ChatScreen.test.tsx`: тот же test-runner и render-хелперы; если проект не на `@testing-library/react`, использовать те же утилиты, что и там):

```tsx
import { render, screen } from "@testing-library/react";
import ZoneChip from "./ZoneChip";

test("рисует лейбл зоны", () => {
  render(<ZoneChip label="центр (ЦАО)" />);
  expect(screen.getByText("центр (ЦАО)")).toBeInTheDocument();
});

test("ничего не рисует без лейбла", () => {
  const { container } = render(<ZoneChip label={null} />);
  expect(container).toBeEmptyDOMElement();
});
```

- [ ] **Step 5: Запустить — FAIL**

Run: `cd frontend && npm test -- ZoneChip`
Expected: FAIL (нет `ZoneChip`).

- [ ] **Step 6: Компонент** — `frontend/components/chat/ZoneChip.tsx`:

```tsx
export default function ZoneChip({ label }: { label: string | null }) {
  if (!label) return null;
  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-zinc-200 bg-white px-3 py-1 text-xs font-medium text-zinc-700 shadow-sm">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
        <path d="M12 21s-6-5.686-6-10a6 6 0 1112 0c0 4.314-6 10-6 10z" />
        <circle cx="12" cy="11" r="2" />
      </svg>
      {label}
    </span>
  );
}
```

- [ ] **Step 7: Вставить чип в `ResultScreen.tsx`** — прочитать `areaLabel` из store (`useSession` уже импортирован в файле) и отрендерить чип над гридом карта+список. Заменить `return` непустого результата:
```tsx
export default function ResultScreen() {
  const properties = useSession((s) => s.properties);
  const areaLabel = useSession((s) => s.areaLabel);
  if (properties.length === 0) {
    return (
      <div className="flex-1 grid place-items-center p-6">
        <EmptyResult />
      </div>
    );
  }
  return (
    <div className="flex-1 flex flex-col gap-3 p-6 overflow-auto">
      <ZoneChip label={areaLabel} />
      <div className="flex-1 grid grid-cols-1 lg:grid-cols-[1.2fr_1fr] gap-6">
        <div className="min-h-[320px] order-first lg:order-none"><MapCanvas /></div>
        <PropertyList />
      </div>
    </div>
  );
}
```
Добавить импорт: `import ZoneChip from "@/components/chat/ZoneChip";`.

- [ ] **Step 8: Запустить — PASS**

Run: `cd frontend && npm test -- ZoneChip && npm test`
Expected: PASS (чип + весь фронт-сьют зелёные).

- [ ] **Step 9: Commit**

```bash
git add frontend/lib/agent/types.ts frontend/lib/api/searchStream.ts frontend/lib/store/session.ts frontend/components/chat/ZoneChip.tsx frontend/components/result/ResultScreen.tsx frontend/components/chat/ZoneChip.test.tsx
git commit -m "feat: чип зоны в выдаче + граница зоны на карте вместо hull"
```

---

### Task 5: Сквозная живая проверка

**Files:** нет кода — операционная проверка на поднятом стеке.

- [ ] **Step 1: Перезапустить нативный ML** (подхватить новый код — uvicorn без --reload):
```bash
pkill -f "uvicorn habitus.online.service"; sleep 2
RETRIEVAL_TOP_K=20 RERANK_MAX_LENGTH=128 nohup uv run uvicorn habitus.online.service:app --host 0.0.0.0 --port 8000 > /tmp/ml.log 2>&1 & disown
```
Прогреть: `curl -s -m 200 -X POST localhost:8000/search -H 'Content-Type: application/json' -d '{"query":"двушка в центре"}' | python -c "import sys,json;d=json.load(sys.stdin);print('area_label',d['area_label'],'geojson?',bool(d['area_geojson']))"`
Expected: `area_label` = «центр (ЦАО)», `geojson? True`.

- [ ] **Step 2: Пересобрать Go/фронт-контейнеры** (если в Docker): `docker compose up -d --build backend frontend`.

- [ ] **Step 3: Браузерный smoke** — реальный Chrome через playwright-core: запрос «двушка в Хамовниках» → чип «Хамовники» над выдачей + полигон района на карте (не клякса). Скриншот. «двушка на севере» → чип «сторона света (САО, СВАО, СЗАО)» + union трёх округов.

- [ ] **Step 4: README** — в раздел про поиск добавить строку про показ зоны (чип + граница на карте). Commit `docs:`.
