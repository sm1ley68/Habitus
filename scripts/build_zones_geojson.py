#!/usr/bin/env python
"""Сборка data/moscow_admin.geojson: полигоны округов (admin_level=5) и районов
(admin_level=8) Москвы из OSM. Ways тянем Overpass'ом, полигоны собираем в PostGIS
(ST_BuildArea). Сложные relation'ы, где сборка не даёт валидный полигон, пропускаем
с логом — их добить ручной выгрузкой osm-boundaries.com при необходимости.

Запуск:  uv run python scripts/build_zones_geojson.py --out data/moscow_admin.geojson
Потом:   uv run habitus import-zones --geojson data/moscow_admin.geojson
"""
import argparse
import json
import re
import time
from pathlib import Path

import psycopg
import requests

from habitus.config import settings
from habitus.geo.osm_extract import HEADERS, OVERPASS_URL, RETRY_STATUS

# Москва как area-фильтр Overpass: OSM relation 102269 → area id 3600102269.
# Синтаксис (area:ID) — именно фильтр набора, не area(ID).
AREA = "(area:3600102269)"


def _zone_name(tags: dict, kind: str) -> str | None:
    """Каноничное имя зоны под резолвер/NLU.
    Округ → короткий код из ref («ЦАО», «САО»); NLU и CARDINAL оперируют ими.
    Район → OSM name без слова «район» («район Хамовники» → «Хамовники»)."""
    if kind == "okrug":
        return tags.get("ref") or tags.get("name")
    name = re.sub(r"^\s*район\s+|\s+район\s*$", "", tags.get("name") or "").strip()
    return name or None


def _overpass(query: str, retries: int = 4, backoff: float = 5.0) -> dict:
    q = f"[out:json][timeout:180];{query}"
    last = ""
    for attempt in range(retries):
        try:
            r = requests.post(OVERPASS_URL, data={"data": q}, headers=HEADERS, timeout=240)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return r.json()
        except (requests.exceptions.RequestException, ValueError) as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass не ответил: {last}")


def _fetch(level: int, kind: str) -> list[dict]:
    """Список relation'ов заданного admin_level с их ways (out geom)."""
    payload = _overpass(
        f'relation["boundary"="administrative"]["admin_level"="{level}"]{AREA};'
        f'out body; way(r);out geom;')
    rels, ways = {}, {}
    for el in payload.get("elements", []):
        if el["type"] == "relation":
            rels[el["id"]] = {"name": _zone_name(el.get("tags") or {}, kind),
                              "members": el.get("members", [])}
    # второй проход: ways по id
    for el in payload.get("elements", []):
        if el["type"] == "way" and el.get("geometry"):
            ways[el["id"]] = [[p["lon"], p["lat"]] for p in el["geometry"]]
    out = []
    for rel in rels.values():
        outer = [ways[m["ref"]] for m in rel["members"]
                 if m.get("type") == "way" and m.get("role") in ("outer", "") and m["ref"] in ways]
        if rel["name"] and outer:
            out.append({"name": rel["name"], "ways": outer})
    return out


def _assemble(conn, ways: list[list]) -> str | None:
    """Собрать полигон из набора линий через PostGIS; None если невалидно."""
    lines = [json.dumps({"type": "LineString", "coordinates": w}) for w in ways]
    row = conn.execute(
        "SELECT ST_AsGeoJSON(ST_Multi(ST_BuildArea(ST_LineMerge(ST_Collect("
        "ARRAY(SELECT ST_SetSRID(ST_GeomFromGeoJSON(x),4326) FROM unnest(%s::text[]) x))))))",
        (lines,)).fetchone()
    return row[0] if row and row[0] else None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", type=Path, default=Path("data/moscow_admin.geojson"))
    args = ap.parse_args()
    feats = []
    with psycopg.connect(settings.db_dsn) as conn:
        for level, kind in ((5, "okrug"), (8, "raion")):
            rels = _fetch(level, kind)
            print(f"admin_level={level}: {len(rels)} relation'ов")
            for rel in rels:
                geom = _assemble(conn, rel["ways"])
                if not geom:
                    print(f"  ПРОПУСК (не собрался полигон): {rel['name']}")
                    continue
                feats.append({"type": "Feature",
                              "properties": {"kind": kind, "name": rel["name"]},
                              "geometry": json.loads(geom)})
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps({"type": "FeatureCollection", "features": feats},
                                   ensure_ascii=False), encoding="utf-8")
    print(f"WROTE {args.out}: {len(feats)} зон")


if __name__ == "__main__":
    main()
