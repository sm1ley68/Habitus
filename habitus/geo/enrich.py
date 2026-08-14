import psycopg
from habitus.config import settings

WALK_SPEED_MPS = 1.33  # средняя пешая скорость

# Опциональный гео-фильтр передаётся БИНДОМ (%(filter_geog)s), не интерполяцией:
#   filter_geog IS NULL         → обогащаем всю таблицу (enrich_all);
#   filter_geog = 'SRID=4326;…'  → только listings в радиусе точки (enrich_around).
# poi_geom_wkt никогда не склеивается в текст запроса — защита от SQL-инъекции.


# Ближайший POI ищем KNN-оператором <-> (по нему работает GIST), но берём пять
# кандидатов, а не одного: <-> упорядочивает по ПЛАНАРНОМУ расстоянию в градусах,
# а планарно ближайший на широте Москвы не всегда геодезически ближайший.
# Итоговое расстояние по-прежнему считается через geography — значения
# сопоставимы с прежними.
# kind подставляется из литерала внутри модуля (module-level constants), не из
# пользовательского ввода — инъекции нет; не вызывать с внешними переменными.
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


def enrich_all(conn: psycopg.Connection) -> int:
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL, {"radius": settings.poi_radius_m, "filter_geog": None})
        n = cur.rowcount
    conn.commit()
    return n


def enrich_around(conn: psycopg.Connection, poi_geom_wkt: str) -> int:
    params = {"radius": settings.poi_radius_m, "filter_geog": f"SRID=4326;{poi_geom_wkt}"}
    with conn.cursor() as cur:
        cur.execute(_ENRICH_SQL, params)
        n = cur.rowcount
    conn.commit()
    return n
