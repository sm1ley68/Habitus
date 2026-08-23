# habitus/clean/normalize.py
import psycopg
from psycopg.rows import dict_row
from psycopg.types.json import Json

# Грубые bbox городов: lon_min, lat_min, lon_max, lat_max.
# msk — в пределах МКАД плюс Новая Москва небольшим запасом.
CITY_BBOX = {
    "msk": (37.30, 55.48, 37.95, 55.95),
    "spb": (29.60, 59.70, 30.70, 60.20),
}

# Историческое имя: на него ссылается код пайплайна и тесты.
MSK_BBOX = CITY_BBOX["msk"]

def is_valid(row: dict) -> bool:
    price = row.get("price") or 0
    area = row.get("area") or 0
    lat, lon = row.get("lat"), row.get("lon")
    if not (1_000_000 <= price <= 3_000_000_000):
        return False
    if not (5 <= area <= 1000):
        return False
    if lat is None or lon is None:
        return False
    # Строка без city — это выхлоп батч-пайплайна Циана, он московский.
    bbox = CITY_BBOX.get(row.get("city") or "msk")
    if bbox is None:
        return False
    lon_min, lat_min, lon_max, lat_max = bbox
    if not (lon_min <= lon <= lon_max and lat_min <= lat <= lat_max):
        return False
    return True

def pick_walk_metro(entries: list[dict]) -> tuple[str | None, float | None]:
    """Ближайшая ПЕШАЯ станция из нормализованного списка source_extra['metro'].

    Записи с mode='transport' игнорируются: это время на транспорте, а колонка
    называется walk_min_metro. Нет пеших записей → (None, None), и дальше
    сработает OSM-фолбэк в enrich.
    """
    walk = [e for e in entries or []
            if e.get("mode") == "walk" and e.get("minutes") is not None]
    if not walk:
        return None, None
    best = min(walk, key=lambda e: e["minutes"])
    return best.get("name") or None, float(best["minutes"])

def promote_to_listings(conn: psycopg.Connection) -> int:
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("SELECT * FROM raw_listings;")
        raws = cur.fetchall()
    valid = [r for r in raws if is_valid(r)]
    for r in valid:
        station, minutes = pick_walk_metro((r.get("source_extra") or {}).get("metro"))
        r["metro_station"], r["walk_min_metro_src"] = station, minutes
        r["source_extra"] = Json(r.get("source_extra") or {})
    sql = """
        INSERT INTO listings
          (external_id, source, price, area, kitchen_area, rooms, level, levels,
           building_type, object_type, geom, description,
           city, address, source_url, source_extra, metro_station, walk_min_metro_src,
           photos)
        VALUES
          (%(external_id)s, %(source)s, %(price)s, %(area)s, %(kitchen_area)s,
           %(rooms)s, %(level)s, %(levels)s, %(building_type)s, %(object_type)s,
           ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326), %(description)s,
           %(city)s, %(address)s, %(source_url)s, %(source_extra)s,
           %(metro_station)s, %(walk_min_metro_src)s, %(photos)s)
        ON CONFLICT (external_id) DO UPDATE SET
           price=EXCLUDED.price, area=EXCLUDED.area, geom=EXCLUDED.geom,
           description=EXCLUDED.description, city=EXCLUDED.city,
           address=EXCLUDED.address, source_url=EXCLUDED.source_url,
           source_extra=EXCLUDED.source_extra, metro_station=EXCLUDED.metro_station,
           walk_min_metro_src=EXCLUDED.walk_min_metro_src,
           photos=EXCLUDED.photos,
           is_active=true, updated_at=now()
        WHERE NOT listings.owner_managed;
    """
    with conn.cursor() as cur:
        cur.executemany(sql, valid)
    conn.commit()
    return len(valid)
