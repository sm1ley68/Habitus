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
