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

-- Exact, source-attributed evidence used by the dossier.  Runtime code never
-- replaces absent values with zero: communal/crime adapters must supply a
-- normalized 0..1 weight, while noise adapters supply an observed dB value.
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
