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

-- Source-attributed evidence used by the dossier and the map layers.  Every
-- layer currently loaded is a MODEL, not a measurement: communal is derived
-- from OSM building start_date, crime from alcohol-outlet density, noise from
-- road classes.  `db` is therefore a modelled value, not an observed one — it
-- must always be published together with `source`.  Runtime code never
-- replaces absent values with zero.
CREATE TABLE IF NOT EXISTS urban_evidence (
    source_id    TEXT NOT NULL,
    source       TEXT NOT NULL,
    city         TEXT NOT NULL CHECK (city IN ('msk', 'spb')),
    layer        TEXT NOT NULL CHECK (layer IN ('communal', 'crime', 'noise')),
    geom         geometry(Geometry, 4326) NOT NULL,
    weight       REAL,
    db           REAL,
    observed_at  TIMESTAMPTZ NOT NULL,
    metadata     JSONB NOT NULL DEFAULT '{}',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source, source_id, layer),
    CHECK (
        (layer IN ('communal', 'crime') AND weight BETWEEN 0 AND 1 AND db IS NULL)
        OR (layer = 'noise' AND db >= 0 AND weight IS NULL)
    )
);
CREATE INDEX IF NOT EXISTS urban_evidence_geom_gix
    ON urban_evidence USING GIST (geom);
-- Обогащение ищет слой шума через ST_DWithin в метрах, то есть по geography.
-- Каст geom::geography делает индекс по geom неприменимым, и запрос сваливается
-- в Seq Scan по 46 тыс. геометрий на каждый объект. Функциональный индекс по
-- касту возвращает поиск на индекс.
CREATE INDEX IF NOT EXISTS urban_evidence_geog_gix
    ON urban_evidence USING GIST ((geom::geography));
CREATE INDEX IF NOT EXISTS urban_evidence_city_layer_ix
    ON urban_evidence (city, layer);

-- Polygonal OSM evidence for obstruction/view classification.  height_m is
-- populated only from an explicit OSM height tag; levels are retained as
-- provenance but are not silently converted to metres.
CREATE TABLE IF NOT EXISTS urban_features (
    osm_type     TEXT NOT NULL,
    osm_id       BIGINT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('building', 'park', 'water')),
    name         TEXT,
    geom         geometry(Geometry, 4326) NOT NULL,
    height_m     REAL,
    levels       INTEGER,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (osm_type, osm_id, kind)
);
CREATE INDEX IF NOT EXISTS urban_features_geom_gix
    ON urban_features USING GIST (geom);
CREATE INDEX IF NOT EXISTS urban_features_kind_ix
    ON urban_features (kind);

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

-- Полигоны админ-деления и колец Москвы (импорт из OSM, разово).
CREATE TABLE IF NOT EXISTS admin_zones (
    id         bigserial PRIMARY KEY,
    kind       text NOT NULL,                 -- 'okrug' | 'raion' | 'ring'
    name       text NOT NULL,                 -- 'ЦАО' | 'Хамовники' | 'Садовое кольцо'
    parent     text,                          -- для raion — имя округа; иначе NULL
    aliases    text[] NOT NULL DEFAULT '{}',
    geom       geometry(MultiPolygon, 4326) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS admin_zones_geom_gix ON admin_zones USING GIST (geom);
CREATE UNIQUE INDEX IF NOT EXISTS admin_zones_kind_name ON admin_zones (kind, lower(name));

-- Курируемый словарь разговорных/брендовых зон (Патрики, Золотая миля…).
CREATE TABLE IF NOT EXISTS named_zones (
    id       bigserial PRIMARY KEY,
    name     text NOT NULL,
    aliases  text[] NOT NULL DEFAULT '{}',
    lon      double precision NOT NULL,
    lat      double precision NOT NULL,
    radius_m double precision NOT NULL DEFAULT 700
);
CREATE UNIQUE INDEX IF NOT EXISTS named_zones_name ON named_zones (lower(name));

-- Предвычисленная принадлежность объявления округу/району.
ALTER TABLE listings ADD COLUMN IF NOT EXISTS okrug text;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS raion text;
CREATE INDEX IF NOT EXISTS listings_okrug_ix ON listings (okrug);
CREATE INDEX IF NOT EXISTS listings_raion_ix ON listings (raion);

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
-- Ссылки на снимки объявления (CDN источника). Явная колонка, а не source_extra:
-- фото участвуют в общем UI — обложка карточки и галерея паспорта.
ALTER TABLE listings ADD COLUMN IF NOT EXISTS photos             text[];
CREATE INDEX IF NOT EXISTS listings_city_ix ON listings (city);

ALTER TABLE poi ADD COLUMN IF NOT EXISTS city text NOT NULL DEFAULT 'msk';
CREATE INDEX IF NOT EXISTS poi_city_kind_ix ON poi (city, kind);

-- raw_listings — зеркало источника: производные поля выводит promote_to_listings.
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS city         text NOT NULL DEFAULT 'msk';
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS address      text;
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS source_url   text;
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS photos       text[];
ALTER TABLE raw_listings ADD COLUMN IF NOT EXISTS source_extra jsonb NOT NULL DEFAULT '{}';

-- Объявление, которым управляет продавец через личный кабинет. Помечает
-- строки, на которые не распространяются два механизма батч-пайплайна:
-- гашение по снимку источника (объявления продавца ни в каком снимке нет)
-- и перезапись полей при повторном обходе (правки продавца главнее источника).
ALTER TABLE listings ADD COLUMN IF NOT EXISTS owner_managed boolean NOT NULL DEFAULT false;

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
    -- true — headway_seconds() из habitus/geo/metro_times.py не нашёл линию в
    -- курируемом файле и вернул пессимистичный дефолт по системе, а не
    -- измеренный интервал. Без этой колонки признак терялся бы на границе с
    -- БД: Задача 9 читает headway_s через SELECT, а не из объекта curated в
    -- памяти, и молча показала бы оценку как факт.
    headway_estimated  BOOLEAN NOT NULL DEFAULT FALSE,
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
