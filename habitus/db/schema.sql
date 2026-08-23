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
