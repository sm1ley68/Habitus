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
        ref = line["ref"]
        headway = int(line["headway_s"])
        speed = float(line["fallback_speed_kmh"])
        if headway <= 0:
            raise ValueError(
                f"{path}: линия {ref!r} имеет headway_s={headway} — "
                f"курированное значение обязано быть положительным")
        if speed <= 0:
            raise ValueError(
                f"{path}: линия {ref!r} имеет fallback_speed_kmh={speed} — "
                f"курированное значение обязано быть положительным")
        c.headways[ref] = headway
        c.speeds[ref] = speed
    for e in raw.get("edges", []):
        seconds = int(e["seconds"])
        if seconds <= 0:
            # Синтетический ноль (или отрицательное значение) в курируемом
            # файле — та же запрещённая подмена «нет замера» на «замер = 0»,
            # только на входе, а не в выдаче. Ловим на загрузке.
            raise ValueError(
                f"{path}: перегон {e['line']!r} {e['from']!r} -> {e['to']!r} "
                f"имеет seconds={seconds} — курированное значение обязано "
                f"быть положительным")
        key = (e["line"], normalize_station_name(e["from"]),
               normalize_station_name(e["to"]))
        c.edges[key] = seconds
        # перегон ненаправленный: поезд идёт столько же в обратную сторону
        c.edges[(e["line"], key[2], key[1])] = seconds
    for t in raw.get("transfers", []):
        seconds = int(t["seconds"])
        if seconds <= 0:
            raise ValueError(
                f"{path}: пересадка {t['from']!r} -> {t['to']!r} имеет "
                f"seconds={seconds} — курированное значение обязано быть "
                f"положительным")
        key = _pair(t["from"], t["to"])
        c.transfers[key] = seconds
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


#: Интервал для линии, которой нет даже в списке lines курируемого файла.
#: Сознательно ПЕССИМИСТИЧНЕЕ шаблонных курированных значений (120/300/600) —
#: некурированная линия не должна тихо унаследовать число, которое выглядит
#: как измеренное. Источник: task-6-brief.md:130, восходит к диапазонам из
#: комментария схемы в task-5-brief.md:93-95.
_UNCURATED_HEADWAY_S = {"subway": 150, "mck": 360, "mcd": 720}


def headway_seconds(curated: CuratedTimes, line_ref: str,
                    system: str) -> tuple[int, bool]:
    """Интервал движения на линии и признак того, что это оценка."""
    if line_ref in curated.headways:
        return curated.headways[line_ref], False
    return _UNCURATED_HEADWAY_S[system], True
