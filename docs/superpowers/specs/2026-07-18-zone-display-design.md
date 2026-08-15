# Явный показ зоны запроса на фронте (чип + полигон)

**Дата:** 2026-07-18
**Статус:** дизайн (спека)

## Проблема

Гео-зоны (`resolve_area`, 2026-07-18) фильтруют выдачу по округу/району, но фронт
зону НЕ показывает: `parsed.area` выкинут в Go-DTO, `RelaxationCard` хардкодит
текст, а `suggested_areas_geojson` на карте — это convex hull результатов, не
граница запрошенной зоны. Пользователь не видит, ЧТО именно было распознано как
область поиска.

## Цель

Показать распознанную зону явно: **текстовый чип** («Центр · ЦАО», «Хамовники»,
«Патриаршие пруды») над выдачей и **реальную границу зоны на карте** вместо
convex-hull. Источник правды — `resolve_area`, который уже знает и лейбл, и
геометрию.

## Решения (утверждены на брейншторминге)

- **Объём:** чип + полигон на карте.
- **Карта:** полигон зоны ЗАМЕНЯЕТ hull, когда зона распознана; без зоны (точка/нет
  area) — прежний hull. Переиспользуем существующий слой `suggested_areas_geojson`
  → ноль изменений в map-слоях фронта.
- **named-зоны** (Патрики, Сити) — круг `ST_Buffer(точка, radius)`, а не админ-полигон
  (у них нет административной границы — by design).
- **Деградация:** зоны не импортированы / геометрия пустая → `area_geojson=None` →
  чип всё равно показывается (лейбл есть), карта откатывается к hull. Геометрия
  НИКОГДА не роняет поиск (try/except в пайплайне).

## Архитектура

Поток через 3 слоя. Геометрия зоны и лейбл рождаются в `geo.py` (единый источник и
для фильтра, и для отрисовки), тонко пробрасываются через Go в SSE `final_result`.

```
resolve_area → AreaMatch(sql, params, label, widen, geom_sql, geom_params)
                                              ↓ (label)          ↓ area_geojson(am, conn)
pipeline → SearchResponse(area_label, area_geojson)
   ↓ Go ml_client SearchResponse(AreaLabel, AreaGeojson)
   ↓ buildFinalResult: area_geojson → suggested_areas_geojson (иначе hull); + area_label
   ↓ SSE final_result → фронт: чип (area_label) + карта (zoneGeoJSON = граница зоны)
```

### 1. ML-слой

**`habitus/online/geo.py`:**
- `AreaMatch` получает два новых поля: `geom_sql: str = ""`, `geom_params: tuple = ()`
  — декларативный источник геометрии зоны как скалярное SQL-выражение. Ставится в
  каждой ветке резолвера (фрагменты SQL — литералы в коде, значения — bind-параметры,
  инъекции нет):
  - **кардинал/центр (округа́):** `geom_sql = "(SELECT ST_Union(geom) FROM admin_zones
    WHERE kind='okrug' AND name = ANY(%s))"`, `geom_params = (list(okrugs),)`. Ветка
    остаётся без запроса к БД — только хранит sql+params.
  - **named-зона:** `geom_sql = "ST_Buffer(ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography,
    %s)::geometry"`, `geom_params = (lon, lat, radius_m)`.
  - **кольцо / имя района / имя округа:** геометрия всегда из строки `admin_zones` по
    `(kind, name)`: `geom_sql = "(SELECT geom FROM admin_zones WHERE kind=%s AND name=%s)"`,
    `geom_params = (kind, name)` (kind ∈ 'ring'|'raion'|'okrug' по ветке).
  - **fallback-полигон:** `geom_sql = "ST_SetSRID(ST_GeomFromGeoJSON(%s),4326)"`, params —
    json геометрии; **fallback-точка:** buffer вокруг точки.
  - Ветки, где геометрию показать нельзя/незачем, оставляют `geom_sql=""` (чип без карты).
- Хелпер `area_geojson(am: AreaMatch, conn) -> dict | None`:
  `SELECT ST_AsGeoJSON(ST_SimplifyPreserveTopology(<geom_sql>, 0.0005), 5)` с
  `geom_params`; парсит в dict; `None`, если строка пуста или результат NULL
  (зоны не импортированы). Упрощение (~55м) + 5 знаков — компактный payload
  (сырые полигоны округов — десятки КБ).

**`habitus/online/schema.py`:** `SearchResponse` += `area_label: str | None = None`,
`area_geojson: dict | None = None`.

**`habitus/online/pipeline.py`:** после резолва области —
`area_label = area_match.label if area_match else None`; `area_geojson` через хелпер
в try/except (ошибка геометрии → None + log.warning, поиск не падает). Оба поля в
`SearchResponse`.

### 2. Go-слой

**`backend/internal/client/ml_client.go`:** DTO `SearchResponse` += `AreaLabel string
\`json:"area_label"\``, `AreaGeojson any \`json:"area_geojson"\``.

**`backend/internal/service/search_stream_service.go`:** в `buildFinalResult` — если
`resp.AreaGeojson != nil`, класть его в `SuggestedAreasGeoJSON` (иначе прежний hull
из `BuildSuggestedAreas`); `FinalResultEvent` += `AreaLabel string \`json:"area_label"\``
из `resp.AreaLabel`.

### 3. Фронт-слой

**`frontend/lib/agent/types.ts`:** `FinalResult`/onDone-payload += `areaLabel: string | null`.

**`frontend/lib/api/searchStream.ts`:** читать `f.data.area_label` из `final_result`,
прокинуть в `onDone` вместе с `zoneGeoJSON` (который теперь = граница зоны).

**`frontend/lib/store/session.ts`:** store += `areaLabel: string | null`; `finish`
проставляет его.

**Чип-компонент** (напр. `frontend/components/chat/ZoneChip.tsx`): маленький
badge с `areaLabel`, рендерится в шапке выдачи/над картой, только когда `areaLabel`
не пуст. Карта уже рисует `zoneGeoJSON` — теперь это реальная граница зоны, изменений
в map-слоях нет.

## Границы модулей

- `geo.py` — единственный источник геометрии зоны (и фильтр-предикат, и `geom_sql`,
  и хелпер `area_geojson`). Пайплайн только зовёт хелпер.
- `schema.py` — форма ответа (источник правды контракта ML↔Go).
- Go — тонкий проброс двух полей + выбор geojson в `buildFinalResult`.
- Фронт — проброс `area_label` в store + один компонент чипа. Map-слои не трогаем.

## Тестирование

- **ML (юниты, scratch-БД `habitus_test` с сидом зон):** ветки резолвера ставят
  `geom_sql`/`geom_params`; `area_geojson` возвращает валидный dict для округа/района/
  named-круга и `None` при пустых `admin_zones`. Пайплайн-тест: зональный запрос →
  `area_label` и `area_geojson` заполнены; не-зональный → оба None.
- **Go:** DTO десериализует `area_label`/`area_geojson`; `buildFinalResult` кладёт
  `area_geojson` в suggested_areas при наличии, иначе hull.
- **Фронт:** `ZoneChip` рендерится при `areaLabel`, скрыт при null; `searchStream`
  прокидывает `area_label`.

## Явно вне охвата (YAGNI)

- Клик по зоне/переход к соседним зонам, легенда зон, выбор зоны на карте вручную.
- Показ метки авто-расширения текстом (`RelaxationCard` остаётся generic — отдельная
  мелкая задача, не блок этой фичи).
- Отдельный слой hull поверх зоны (решено: полигон заменяет hull).

## Открытых пунктов нет.
