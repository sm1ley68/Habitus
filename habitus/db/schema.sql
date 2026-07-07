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
