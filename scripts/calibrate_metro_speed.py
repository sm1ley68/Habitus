"""Калибровка fallback_speed_kmh линий метро по OSM duration.

Зачем. Времена перегонов в metro_edge почти все модельные: курируемый файл
data/metro/<city>.json содержит единицы явных перегонов, остальное считает
edge_seconds() как «расстояние между станциями / скорость линии + стоянка».
Скорость при этом стояла плоской константой 40 км/ч у ВСЕХ линий метро —
это была догадка, а не замер.

У маршрутных отношений OSM (`type=route`, `route=subway`) есть тег `duration`
— время от конечной до конечной. Это реальные данные, и из них скорость
выводится точно в той форме, в какой её потребляет модель:

    duration = Σ(метры_ребра / v) + рёбра * STOP_DWELL_S
    v = Σ метры / (duration - рёбра * STOP_DWELL_S)

Метры берутся тем же ST_Distance между точками станций, что и в
habitus/geo/metro.py: скорость здесь — «эффективная по прямой», она сама
поглощает кривизну путей, и подменять её паспортной скоростью поезда нельзя.

Чего скрипт НЕ делает: он не превращает перегоны в замеры. Каждый перегон
по-прежнему оценка (estimated=True), просто теперь оценка по измеренной
скорости своей линии, а не по общей догадке. Снять флаг может только
курирование пооперегонных времён из источника, которого у проекта нет.

Линии без duration в OSM (МЦК, МЦД, часть новых) не трогаются: выдумывать им
скорость нельзя, у них остаётся прежнее курированное значение.

Запуск:  uv run python scripts/calibrate_metro_speed.py [--write]
Без --write только печатает сравнение.
"""
import argparse
import collections
import json
from pathlib import Path

import psycopg
import requests

from habitus.config import settings
from habitus.geo.metro_times import STOP_DWELL_S

OVERPASS = "https://overpass-api.de/api/interpreter"
HEADERS = {"User-Agent": "Habitus/1.0 (real-estate research)"}
BBOX = {"msk": "(55.48,37.30,55.95,37.95)", "spb": "(59.70,29.60,60.20,30.70)"}


def osm_durations(city: str) -> dict[str, float]:
    """ref линии → минимальная duration в минутах среди её маршрутов.

    Минимум, а не среднее: у линии два направления и иногда укороченные
    маршруты; берём самый быстрый полный проезд, потому что укороченный даст
    заниженную длительность только вместе с заниженной длиной, а среднее
    смешает разные трассы.
    """
    query = (f'[out:json][timeout:90];'
             f'rel{BBOX[city]}["type"="route"]["route"="subway"];out tags;')
    resp = requests.post(OVERPASS, data={"data": query}, headers=HEADERS, timeout=120)
    resp.raise_for_status()
    out: dict[str, list[float]] = collections.defaultdict(list)
    for el in resp.json()["elements"]:
        tags = el.get("tags", {})
        raw, ref = tags.get("duration"), tags.get("ref")
        if not raw or not ref:
            continue
        if ":" in raw:
            parts = raw.split(":")
            minutes = int(parts[0]) * 60 + int(parts[1])
        else:
            try:
                minutes = float(raw)
            except ValueError:
                continue
        out[ref].append(minutes)
    return {ref: min(v) for ref, v in out.items()}


def line_geometry(conn, city: str) -> dict[str, tuple[float, int]]:
    """ref линии → (сумма метров по одному направлению, число рёбер)."""
    rows = conn.execute("""
        SELECT l.ref,
               sum(ST_Distance(a.geom::geography, b.geom::geography)) / 2,
               count(*) / 2
        FROM metro_edge e
        JOIN metro_station a ON a.id = e.from_station
        JOIN metro_station b ON b.id = e.to_station
        JOIN metro_line l ON l.id = a.line_id AND l.id = b.line_id
        WHERE e.city = %s
        GROUP BY l.ref;""", (city,)).fetchall()
    return {ref: (float(m), int(n)) for ref, m, n in rows if m}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--city", default="msk")
    ap.add_argument("--write", action="store_true",
                    help="записать новые скорости в data/metro/<city>.json")
    args = ap.parse_args()

    durations = osm_durations(args.city)
    with psycopg.connect(settings.db_dsn) as conn:
        geometry = line_geometry(conn, args.city)

    path = Path(settings.data_dir) / "metro" / f"{args.city}.json"
    raw = json.loads(path.read_text(encoding="utf-8"))

    print(f"{'линия':7} {'рёбер':>6} {'км':>7} {'OSM мин':>8} "
          f"{'было':>6} {'стало':>7} {'Δ времени':>10}")
    changed = 0
    for line in raw["lines"]:
        ref = line["ref"]
        if ref not in durations or ref not in geometry:
            print(f"{ref:7} {'':>6} {'':>7} {'нет duration в OSM':>8} "
                  f"{line['fallback_speed_kmh']:>6} {'—':>7}")
            continue
        metres, edges = geometry[ref]
        running_s = durations[ref] * 60 - edges * STOP_DWELL_S
        if running_s <= 0:
            print(f"{ref:7} стоянки съедают всю duration — пропуск")
            continue
        speed = metres / running_s * 3.6
        was = float(line["fallback_speed_kmh"])
        # Насколько изменится модельное время перегона (без стоянки).
        delta = (was / speed - 1) * 100
        print(f"{ref:7} {edges:>6} {metres/1000:>7.1f} {durations[ref]:>8.0f} "
              f"{was:>6.0f} {speed:>7.1f} {delta:>+9.0f}%")
        if args.write:
            line["fallback_speed_kmh"] = round(speed, 1)
            line["speed_source"] = "OSM route duration"
            changed += 1

    if args.write:
        path.write_text(json.dumps(raw, ensure_ascii=False, indent=2) + "\n",
                        encoding="utf-8")
        print(f"\nзаписано в {path}: откалибровано линий {changed}, "
              f"остальные оставлены как были")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
