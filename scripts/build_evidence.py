#!/usr/bin/env python
"""Сборка GeoJSON слоёв urban_evidence (communal/crime/noise) из реальных источников.

Точных открытых геоданных по коммуналкам/преступности/шуму для Москвы нет,
поэтому слои выводятся из реальной геометрии по документированным прокси
(методика — в комментарии каждого сборщика). Никакой синтетики: каждая фича
привязана к реальному OSM-объекту или POI из нашей БД, source честно называет
методику, вес/дБ — из открытой литературы.

  noise    — крупные дороги и рельсы OSM (LineString), дБ по классу трассы:
             городской шум доминируется трафиком (стандартный акустический прокси).
  crime    — буферы вокруг баров/алкоточек из нашей poi, вес = нормированная
             плотность соседей (связь alcohol-outlet density и уличной
             преступности задокументирована в криминологии).
  communal — контуры зданий OSM со start_date до 1960: коммуналки — это
             дореволюционный и ранне-советский фонд («уплотнение»), вес по эпохе.

Запуск:  uv run python scripts/build_evidence.py --out data/evidence_msk.geojson
Потом:   uv run habitus import-evidence --geojson data/evidence_msk.geojson
"""
import argparse
import json
import re
import time
from datetime import datetime, timezone
from pathlib import Path

import psycopg
import requests

from habitus.config import settings
from habitus.geo.osm_extract import HEADERS, MSK_AREA, OVERPASS_URL, RETRY_STATUS

# --- шумовые уровни по классу трассы, дБ на кромке (открытые справочники
# дорожной акустики; затухание до фасада досье считает усреднением по 500 м) ---
NOISE_DB = {"motorway": 75, "trunk": 72, "primary": 70,
            "secondary": 65, "tertiary": 60, "rail": 70, "tram": 65}

# --- вес communal-риска по эпохе постройки ---
#   ≤1917 доходные дома → массовое «уплотнение» после 1918
#   1918–1940 конструктивизм/ранние сталинки → коммунальное заселение по проекту
#   1941–1959 поздние сталинки → частично коммунальные
#   1960+ хрущёвки и далее → посемейное заселение, не риск (пропускаем)
COMMUNAL_ERAS = [(1917, 0.9), (1940, 0.7), (1959, 0.5)]

CRIME_BUFFER_M = 200      # радиус влияния точки
CRIME_DENSITY_M = 300     # окно подсчёта плотности соседей


def _overpass(query: str, retries: int = 4, backoff: float = 5.0) -> dict:
    q = f"[out:json][timeout:300];{query}"
    last = ""
    for attempt in range(retries):
        try:
            r = requests.post(OVERPASS_URL, data={"data": q}, headers=HEADERS,
                              timeout=360)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return r.json()
        except (requests.exceptions.RequestException, ValueError) as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass не ответил за {retries} попыток: {last}")


def _line(el: dict) -> list | None:
    coords = [[p["lon"], p["lat"]] for p in el.get("geometry") or []
              if p.get("lon") is not None]
    return coords if len(coords) >= 2 else None


def _ring(el: dict) -> list | None:
    coords = _line(el)
    if not coords or len(coords) < 3:
        return None
    if coords[0] != coords[-1]:
        coords.append(coords[0])
    return coords if len(coords) >= 4 else None


def build_noise(observed_at: str) -> list[dict]:
    """Дороги motorway..tertiary + rail/tram → LineString с дБ по классу."""
    payload = _overpass(
        f'(way["highway"~"^(motorway|trunk|primary|secondary|tertiary)$"]{MSK_AREA};'
        f'way["railway"~"^(rail|tram)$"]{MSK_AREA};);out tags geom;')
    feats = []
    for el in payload.get("elements", []):
        tags = el.get("tags") or {}
        cls = tags.get("highway") or tags.get("railway")
        db = NOISE_DB.get(cls)
        line = _line(el)
        if db is None or line is None:
            continue
        feats.append({
            "type": "Feature",
            "properties": {"source_id": f"way/{el['id']}",
                           "source": "osm-traffic-noise-proxy", "city": "msk",
                           "layer": "noise", "db": db, "class": cls,
                           "observed_at": observed_at},
            "geometry": {"type": "LineString", "coordinates": line}})
    return feats


def build_communal(observed_at: str) -> list[dict]:
    """Здания OSM со start_date ≤ 1959 → контур-полигон, вес по эпохе."""
    payload = _overpass(f'way["building"]["start_date"]{MSK_AREA};out tags geom;')
    feats = []
    for el in payload.get("elements", []):
        m = re.search(r"1[6-9]\d\d", str((el.get("tags") or {}).get("start_date")))
        ring = _ring(el)
        if m is None or ring is None:
            continue
        year = int(m.group())
        weight = next((w for cap, w in COMMUNAL_ERAS if year <= cap), None)
        if weight is None:
            continue
        feats.append({
            "type": "Feature",
            "properties": {"source_id": f"way/{el['id']}",
                           "source": "osm-start-date-communal-proxy",
                           "city": "msk", "layer": "communal", "weight": weight,
                           "year": year, "observed_at": observed_at},
            "geometry": {"type": "Polygon", "coordinates": [ring]}})
    return feats


def build_crime(conn: psycopg.Connection, observed_at: str) -> list[dict]:
    """Буферы вокруг bar/alcohol из poi; вес = плотность соседей / p95 (кап 1)."""
    with conn.cursor() as cur:
        cur.execute(f"""
            SELECT p.kind, p.osm_id,
                   ST_AsGeoJSON(ST_Buffer(p.geom::geography,
                                          {CRIME_BUFFER_M})::geometry, 6),
                   (SELECT count(*) FROM poi n
                    WHERE n.kind IN ('bar','alcohol')
                      AND ST_DWithin(n.geom::geography, p.geom::geography,
                                     {CRIME_DENSITY_M}))
            FROM poi p WHERE p.kind IN ('bar','alcohol');""")
        rows = cur.fetchall()
    densities = sorted(r[3] for r in rows)
    p95 = densities[int(len(densities) * 0.95)] if densities else 1
    feats = []
    for kind, osm_id, geom, density in rows:
        feats.append({
            "type": "Feature",
            "properties": {"source_id": f"{kind}/{osm_id}",
                           "source": "poi-alcohol-density-crime-proxy",
                           "city": "msk", "layer": "crime",
                           "weight": round(min(1.0, density / p95), 3),
                           "density": density, "observed_at": observed_at},
            "geometry": json.loads(geom)})
    return feats


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", type=Path,
                    default=settings.data_dir / "evidence_msk.geojson")
    args = ap.parse_args()
    observed_at = datetime.now(timezone.utc).isoformat()

    with psycopg.connect(settings.db_dsn) as conn:
        crime = build_crime(conn, observed_at)
    print(f"crime:    {len(crime)} фич (буферы {CRIME_BUFFER_M} м из poi)")
    communal = build_communal(observed_at)
    print(f"communal: {len(communal)} фич (start_date ≤ 1959 из OSM)")
    noise = build_noise(observed_at)
    print(f"noise:    {len(noise)} фич (дороги/рельсы из OSM)")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    # пишем фичи потоково — коллекция большая, не собираем гигантскую строку
    with open(args.out, "w", encoding="utf-8") as f:
        f.write('{"type":"FeatureCollection","features":[\n')
        first = True
        for feat in (*crime, *communal, *noise):
            if not first:
                f.write(",\n")
            json.dump(feat, f, ensure_ascii=False, separators=(",", ":"))
            first = False
        f.write("\n]}\n")
    print(f"WROTE {args.out} ({args.out.stat().st_size / 1e6:.1f} МБ)")


if __name__ == "__main__":
    main()
