import time
import json
import re

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

URBAN_FEATURE_QUERY = (
    f'(way["building"]{MSK_AREA};'
    f'way["leisure"="park"]{MSK_AREA};'
    f'way["natural"="water"]{MSK_AREA};'
    f'way["waterway"="riverbank"]{MSK_AREA};);'
)

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


def _number(value):
    if value is None:
        return None
    match = re.search(r"-?\d+(?:[.,]\d+)?", str(value))
    return float(match.group().replace(",", ".")) if match else None


def parse_urban_features(payload: dict) -> list[dict]:
    rows = []
    for el in payload.get("elements", []):
        geometry = el.get("geometry") or []
        coords = [[p.get("lon"), p.get("lat")] for p in geometry
                  if p.get("lon") is not None and p.get("lat") is not None]
        if len(coords) < 3:
            continue
        if coords[0] != coords[-1]:
            coords.append(coords[0])
        tags = el.get("tags") or {}
        if "building" in tags:
            kind = "building"
        elif tags.get("leisure") == "park":
            kind = "park"
        else:
            kind = "water"
        levels = _number(tags.get("building:levels"))
        rows.append({
            "osm_type": el.get("type", "way"), "osm_id": el["id"],
            "kind": kind, "name": tags.get("name"),
            "geometry": json.dumps({"type": "Polygon", "coordinates": [coords]}),
            "height_m": _number(tags.get("height")),
            "levels": int(levels) if levels is not None and levels >= 0 else None,
        })
    return rows


def fetch_urban_features(http_post=requests.post, retries: int = 4,
                         backoff: float = 3.0) -> list[dict]:
    q = f"[out:json][timeout:300];{URBAN_FEATURE_QUERY}out tags geom;"
    last = ""
    for attempt in range(retries):
        try:
            r = http_post(OVERPASS_URL, data={"data": q}, headers=HEADERS,
                          timeout=360)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return parse_urban_features(r.json())
        except requests.exceptions.RequestException as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass urban features failed after {retries} attempts: {last}")

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


def upsert_urban_features(rows: list[dict], conn: psycopg.Connection) -> int:
    sql = """
        INSERT INTO urban_features
            (osm_type, osm_id, kind, name, geom, height_m, levels)
        VALUES
            (%(osm_type)s, %(osm_id)s, %(kind)s, %(name)s,
             ST_SetSRID(ST_GeomFromGeoJSON(%(geometry)s), 4326),
             %(height_m)s, %(levels)s)
        ON CONFLICT (osm_type, osm_id, kind) DO UPDATE SET
            name=EXCLUDED.name, geom=EXCLUDED.geom,
            height_m=EXCLUDED.height_m, levels=EXCLUDED.levels,
            updated_at=now();
    """
    if rows:
        with conn.cursor() as cur:
            cur.executemany(sql, rows)
    conn.commit()
    return len(rows)
