# habitus/clean/normalize.py
import psycopg
from psycopg.rows import dict_row

# грубый bbox Москвы (в пределах МКАД + Новая Москва небольшим запасом)
MSK_BBOX = (37.30, 55.48, 37.95, 55.95)  # lon_min, lat_min, lon_max, lat_max

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
    lon_min, lat_min, lon_max, lat_max = MSK_BBOX
    if not (lon_min <= lon <= lon_max and lat_min <= lat <= lat_max):
        return False
    return True

def promote_to_listings(conn: psycopg.Connection) -> int:
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("SELECT * FROM raw_listings;")
        raws = cur.fetchall()
    valid = [r for r in raws if is_valid(r)]
    sql = """
        INSERT INTO listings
          (external_id, source, price, area, kitchen_area, rooms, level, levels,
           building_type, object_type, geom, description)
        VALUES
          (%(external_id)s, %(source)s, %(price)s, %(area)s, %(kitchen_area)s,
           %(rooms)s, %(level)s, %(levels)s, %(building_type)s, %(object_type)s,
           ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326), %(description)s)
        ON CONFLICT (external_id) DO UPDATE SET
           price=EXCLUDED.price, area=EXCLUDED.area, geom=EXCLUDED.geom,
           description=EXCLUDED.description, is_active=true, updated_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, valid)
    conn.commit()
    return len(valid)
