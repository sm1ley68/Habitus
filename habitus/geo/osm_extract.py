import time

import requests
import psycopg

OVERPASS_URL = "https://overpass-api.de/api/interpreter"
MSK_AREA = "(55.48,37.30,55.95,37.95)"  # bbox: south,west,north,east

# Overpass отдаёт 406 Not Acceptable на дефолтный python-requests UA — нужен
# осмысленный User-Agent, иначе живой фетч POI не работает.
HEADERS = {"User-Agent": "Habitus/1.0 (real-estate research)"}

# публичный Overpass под нагрузкой отдаёт транзиентные 429/502/503/504 —
# ретраим с backoff, иначе один timeout роняет весь offline-прогон.
RETRY_STATUS = {429, 502, 503, 504}

OVERPASS_QUERIES = {
    "school":     f'node["amenity"="school"]{MSK_AREA};',
    "bar":        f'node["amenity"~"bar|pub"]{MSK_AREA};',
    "alcohol":    f'node["shop"="alcohol"]{MSK_AREA};',
    # парки в OSM — полигоны (way/relation), а не точки; берём и их центроид.
    "park":       f'(node["leisure"="park"]{MSK_AREA};'
                  f'way["leisure"="park"]{MSK_AREA};'
                  f'relation["leisure"="park"]{MSK_AREA};);',
    "metro":      f'node["station"="subway"]{MSK_AREA};',
}

def parse_overpass(kind: str, payload: dict) -> list[dict]:
    rows = []
    for el in payload.get("elements", []):
        # node — координаты прямо; way/relation при `out center` — в el["center"].
        if el.get("type") == "node":
            lat, lon = el.get("lat"), el.get("lon")
        else:
            center = el.get("center") or {}
            lat, lon = center.get("lat"), center.get("lon")
        if lat is None or lon is None:
            continue
        rows.append({
            "osm_id": el["id"],
            "kind": kind,
            "name": el.get("tags", {}).get("name"),
            "lat": lat,
            "lon": lon,
        })
    return rows

def fetch_kind(kind: str, http_post=requests.post, retries: int = 4,
               backoff: float = 3.0) -> list[dict]:
    # POST надёжнее GET на крупных запросах; [timeout:120] — серверный лимит Overpass.
    # `out center;` — для way/relation отдаёт центроид, для node просто координаты.
    q = f"[out:json][timeout:120];{OVERPASS_QUERIES[kind]}out center;"
    last = ""
    for attempt in range(retries):
        try:
            r = http_post(OVERPASS_URL, data={"data": q}, headers=HEADERS,
                          timeout=180)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return parse_overpass(kind, r.json())
        except requests.exceptions.RequestException as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass '{kind}' не удался за {retries} попыток: {last}")

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
