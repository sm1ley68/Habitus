import requests
import psycopg

OVERPASS_URL = "https://overpass-api.de/api/interpreter"
MSK_AREA = "(55.48,37.30,55.95,37.95)"  # bbox: south,west,north,east

OVERPASS_QUERIES = {
    "school":     f'node["amenity"="school"]{MSK_AREA};',
    "bar":        f'node["amenity"~"bar|pub"]{MSK_AREA};',
    "alcohol":    f'node["shop"="alcohol"]{MSK_AREA};',
    "park":       f'node["leisure"="park"]{MSK_AREA};',
    "metro":      f'node["station"="subway"]{MSK_AREA};',
}

def parse_overpass(kind: str, payload: dict) -> list[dict]:
    rows = []
    for el in payload.get("elements", []):
        if el.get("type") != "node":
            continue
        rows.append({
            "osm_id": el["id"],
            "kind": kind,
            "name": el.get("tags", {}).get("name"),
            "lat": el["lat"],
            "lon": el["lon"],
        })
    return rows

def fetch_kind(kind: str, http_get=requests.get) -> list[dict]:
    q = f"[out:json][timeout:60];{OVERPASS_QUERIES[kind]}out;"
    r = http_get(OVERPASS_URL, params={"data": q}, timeout=90)
    r.raise_for_status()
    return parse_overpass(kind, r.json())

def upsert_poi(rows: list[dict], conn: psycopg.Connection) -> int:
    sql = """
        INSERT INTO poi (osm_id, kind, name, geom)
        VALUES (%(osm_id)s, %(kind)s, %(name)s,
                ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326))
        ON CONFLICT (osm_id, kind) DO UPDATE SET
            name=EXCLUDED.name, geom=EXCLUDED.geom, updated_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, rows)
    conn.commit()
    return len(rows)
