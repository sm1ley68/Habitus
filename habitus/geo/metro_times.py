# habitus/geo/metro_times.py — курируемый слой времён поверх топологии из OSM.
#
# Ключ сопоставления — нормализованное имя станции плюс линия, НЕ osm_id:
# правка разметки в OSM не должна обнулять курированные данные.
import json
from dataclasses import dataclass, field
from pathlib import Path

from habitus.config import settings
from habitus.geo.metro import normalize_station_name

#: Пересадка, которой нет в курируемом файле. Три минуты — типовой подземный
#: переход; уличные переходы между системами существенно длиннее и обязаны
#: курироваться явно (вывести их из геометрии нельзя).
DEFAULT_TRANSFER_S = 180
#: Стоянка на станции, добавляемая к оценочному перегону.
STOP_DWELL_S = 25


@dataclass
class CuratedTimes:
    headways: dict[str, int] = field(default_factory=dict)
    speeds: dict[str, float] = field(default_factory=dict)
    edges: dict[tuple[str, str, str], int] = field(default_factory=dict)
    transfers: dict[tuple[str, str], int] = field(default_factory=dict)
    outdoor: set[tuple[str, str]] = field(default_factory=set)


def _pair(a: str, b: str) -> tuple[str, str]:
    """Ненаправленный ключ: пересадка одинакова в обе стороны."""
    x, y = normalize_station_name(a), normalize_station_name(b)
    return (x, y) if x <= y else (y, x)


def load_curated(city: str, data_dir: Path | None = None) -> CuratedTimes:
    path = (data_dir or settings.data_dir) / "metro" / f"{city}.json"
    raw = json.loads(path.read_text(encoding="utf-8"))
    c = CuratedTimes()
    for line in raw.get("lines", []):
        c.headways[line["ref"]] = int(line["headway_s"])
        c.speeds[line["ref"]] = float(line["fallback_speed_kmh"])
    for e in raw.get("edges", []):
        key = (e["line"], normalize_station_name(e["from"]),
               normalize_station_name(e["to"]))
        c.edges[key] = int(e["seconds"])
        # перегон ненаправленный: поезд идёт столько же в обратную сторону
        c.edges[(e["line"], key[2], key[1])] = int(e["seconds"])
    for t in raw.get("transfers", []):
        key = _pair(t["from"], t["to"])
        c.transfers[key] = int(t["seconds"])
        if t.get("outdoor"):
            c.outdoor.add(key)
    return c


def edge_seconds(curated: CuratedTimes, line_ref: str, a_name: str, b_name: str,
                 system: str, metres: float) -> tuple[int, bool]:
    """Секунды перегона и признак того, что это оценка, а не курированное значение."""
    key = (line_ref, normalize_station_name(a_name), normalize_station_name(b_name))
    if key in curated.edges:
        return curated.edges[key], False
    # Фолбэк: расстояние по геометрии линии на скорость ЭТОЙ линии. Одна
    # константа на все три системы врала бы систематически — у диаметров
    # перегонная скорость заметно выше метро.
    kmh = curated.speeds.get(line_ref) or _DEFAULT_SPEED_KMH[system]
    return int(round(metres / (kmh * 1000 / 3600))) + STOP_DWELL_S, True


#: Запасная скорость, когда линии нет даже в списке lines курируемого файла
#: (например, станция открылась и приехала из OSM раньше, чем её докурировали).
_DEFAULT_SPEED_KMH = {"subway": 40.0, "mck": 45.0, "mcd": 55.0}


def transfer_seconds(curated: CuratedTimes, a_name: str,
                     b_name: str) -> tuple[int, bool, bool]:
    """Секунды пересадки, признак оценки и признак уличного перехода."""
    key = _pair(a_name, b_name)
    if key in curated.transfers:
        return curated.transfers[key], False, key in curated.outdoor
    return DEFAULT_TRANSFER_S, True, False
