import time
import json
import re

import requests
import psycopg

OVERPASS_URL = "https://overpass-api.de/api/interpreter"

# Overpass отдаёт 406 Not Acceptable на дефолтный python-requests UA — нужен
# осмысленный User-Agent, иначе живой фетч POI не работает.
HEADERS = {"User-Agent": "Habitus/1.0 (real-estate research)"}

# публичный Overpass под нагрузкой отдаёт транзиентные 429/502/503/504 —
# ретраим с backoff, иначе один timeout роняет весь offline-прогон.
RETRY_STATUS = {429, 502, 503, 504}

# bbox в формате Overpass: (south,west,north,east). Зеркало CITY_BBOX из
# habitus/clean/normalize.py и frontend/lib/city.ts — там порядок другой
# ([lng_min, lat_min, lng_max, lat_max]), сверять по значениям, не по позициям.
CITY_AREA = {
    "msk": "(55.48,37.30,55.95,37.95)",
    "spb": "(59.70,29.60,60.20,30.70)",
}

# Транспортный bbox шире городского: МЦД уходят далеко за Москву (D1 до
# Одинцова и Лобни, D3 от Зеленограда до Раменского), и городской bbox рвал бы
# линию посередине — граф получился бы несвязным. Объявления в этот bbox не
# попадают: он используется ИСКЛЮЧИТЕЛЬНО построением транспортного графа.
TRANSIT_AREA = {
    "msk": "(55.00,36.60,56.30,38.60)",
    "spb": CITY_AREA["spb"],   # диаметров нет — расширять нечего
}

POI_KINDS = ("school", "bar", "alcohol", "park", "metro")


def overpass_queries(area: str) -> dict[str, str]:
    """Фрагменты Overpass-запросов по слоям POI для конкретного bbox."""
    return {
        # Школьные здания в OSM — way/relation, а не node: node-only запрос давал
        # 173 школы на Москву вместо ~1500, и walk_min_school врал.
        "school":  f'(node["amenity"="school"]{area};'
                   f'way["amenity"="school"]{area};'
                   f'relation["amenity"="school"]{area};);',
        "bar":     f'node["amenity"~"bar|pub"]{area};',
        "alcohol": f'node["shop"="alcohol"]{area};',
        # парки в OSM — полигоны (way/relation), а не точки; берём и их центроид.
        "park":    f'(node["leisure"="park"]{area};'
                   f'way["leisure"="park"]{area};'
                   f'relation["leisure"="park"]{area};);',
        "metro":   f'node["station"="subway"]{area};',
    }


_MSK = CITY_AREA["msk"]
URBAN_FEATURE_QUERY = (
    f'(way["building"]{_MSK};'
    f'way["leisure"="park"]{_MSK};'
    f'way["natural"="water"]{_MSK};'
    f'way["waterway"="riverbank"]{_MSK};);'
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

def fetch_kind(kind: str, city: str = "msk", http_post=requests.post,
               retries: int = 4, backoff: float = 3.0) -> list[dict]:
    # POST надёжнее GET на крупных запросах; [timeout:120] — серверный лимит Overpass.
    # `out center;` — для way/relation отдаёт центроид, для node просто координаты.
    q = f"[out:json][timeout:120];{overpass_queries(CITY_AREA[city])[kind]}out center;"
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

def upsert_poi(rows: list[dict], conn: psycopg.Connection, city: str = "msk") -> int:
    sql = """
        INSERT INTO poi (osm_id, kind, name, geom, city)
        VALUES (%(osm_id)s, %(kind)s, %(name)s,
                ST_SetSRID(ST_MakePoint(%(lon)s, %(lat)s), 4326), %(city)s)
        ON CONFLICT (osm_id, kind) DO UPDATE SET
            name=EXCLUDED.name, geom=EXCLUDED.geom, city=EXCLUDED.city,
            updated_at=now();
    """
    with conn.cursor() as cur:
        cur.executemany(sql, [{**r, "city": city} for r in rows])
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
