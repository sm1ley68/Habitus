# habitus/geo/zones.py — импорт полигонов админ-зон/словаря и backfill объявлений
import json
from pathlib import Path

import psycopg


def import_admin_geojson(path: Path, conn: psycopg.Connection) -> int:
    """FeatureCollection округов/районов/колец → admin_zones (идемпотентно)."""
    fc = json.loads(Path(path).read_text(encoding="utf-8"))
    rows = []
    for f in fc.get("features", []):
        p = f.get("properties") or {}
        kind, name = p.get("kind"), p.get("name")
        geom = f.get("geometry")
        if kind not in ("okrug", "raion", "ring") or not name or not geom:
            continue
        rows.append({"kind": kind, "name": name,
                     "aliases": p.get("aliases") or [],
                     "geometry": json.dumps(geom)})
    sql = """
        INSERT INTO admin_zones (kind, name, aliases, geom)
        VALUES (%(kind)s, %(name)s, %(aliases)s,
                ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(%(geometry)s), 4326)))
        ON CONFLICT (kind, lower(name)) DO UPDATE SET
            aliases=EXCLUDED.aliases, geom=EXCLUDED.geom, updated_at=now();
    """
    if rows:
        with conn.cursor() as cur:
            cur.executemany(sql, rows)
    conn.commit()
    return len(rows)


def import_named_seed(path: Path, conn: psycopg.Connection) -> int:
    """JSON-массив разговорных зон → named_zones (идемпотентно по name)."""
    items = json.loads(Path(path).read_text(encoding="utf-8"))
    sql = """
        INSERT INTO named_zones (name, aliases, lon, lat, radius_m)
        VALUES (%(name)s, %(aliases)s, %(lon)s, %(lat)s, %(radius_m)s)
        ON CONFLICT (lower(name)) DO UPDATE SET
            aliases=EXCLUDED.aliases, lon=EXCLUDED.lon, lat=EXCLUDED.lat,
            radius_m=EXCLUDED.radius_m;
    """
    rows = [{"name": it["name"], "aliases": it.get("aliases") or [],
             "lon": it["lon"], "lat": it["lat"],
             "radius_m": it.get("radius_m", 700)} for it in items]
    if rows:
        with conn.cursor() as cur:
            cur.executemany(sql, rows)
    conn.commit()
    return len(rows)


def backfill_listing_zones(conn: psycopg.Connection) -> int:
    """Проставить listings.okrug/raion через ST_Contains + parent района округом."""
    with conn.cursor() as cur:
        # parent района = округ, содержащий его центроид
        cur.execute("""
            UPDATE admin_zones r SET parent = o.name
            FROM admin_zones o
            WHERE r.kind='raion' AND o.kind='okrug'
              AND ST_Contains(o.geom, ST_PointOnSurface(r.geom));""")
        cur.execute("""
            UPDATE listings l SET
              okrug = (SELECT name FROM admin_zones z WHERE z.kind='okrug'
                       AND ST_Contains(z.geom, l.geom) LIMIT 1),
              raion = (SELECT name FROM admin_zones z WHERE z.kind='raion'
                       AND ST_Contains(z.geom, l.geom) LIMIT 1)
            WHERE l.geom IS NOT NULL;""")
        n = cur.rowcount
    conn.commit()
    return n
