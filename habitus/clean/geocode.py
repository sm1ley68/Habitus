# habitus/clean/geocode.py
import time
from functools import lru_cache
import requests
import psycopg

NOMINATIM = "https://nominatim.openstreetmap.org/search"

@lru_cache(maxsize=10000)
def geocode_address(addr: str, session=None) -> tuple[float, float] | None:
    for attempt in range(3):
        try:
            r = requests.get(
                NOMINATIM,
                params={"q": addr, "format": "json", "limit": 1, "countrycodes": "ru"},
                headers={"User-Agent": "habitus-ml/0.1"},
                timeout=10,
            )
            r.raise_for_status()
            data = r.json()
            if not data:
                return None
            time.sleep(1.0)  # уважение лимита Nominatim (1 req/s)
            return (float(data[0]["lon"]), float(data[0]["lat"]))
        except (requests.RequestException, ValueError, KeyError):
            time.sleep(2 ** attempt)
    return None

def backfill_missing_coords(conn: psycopg.Connection, geocoder=geocode_address) -> int:
    with conn.cursor() as cur:
        cur.execute("""SELECT external_id, description FROM listings
                       WHERE geom IS NULL AND description IS NOT NULL;""")
        rows = cur.fetchall()
    updated = 0
    for ext_id, addr in rows:
        res = geocoder(addr)
        if res is None:
            continue
        lon, lat = res
        with conn.cursor() as cur:
            cur.execute("""UPDATE listings
                           SET geom=ST_SetSRID(ST_MakePoint(%s,%s),4326), updated_at=now()
                           WHERE external_id=%s;""", (lon, lat, ext_id))
        updated += 1
    conn.commit()
    return updated
