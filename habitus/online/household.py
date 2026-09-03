"""Точки домохозяйства: метка поездки («Лицей 239», «Сити») → координата.

Единственное место, где метка из ParsedQuery.household превращается в точку.
Раньше это умело только досье, и из-за этого состав семьи влиял на объяснение
объекта, но никак не влиял на то, какие объекты вообще попадут в выдачу, —
главный пункт УТП («учитывается жизнь домохозяйства, а не одна точка») жил
только на экране досье.

Правила геокодирования здесь ровно те же, что были в dossier.py, и живут они
теперь в одном экземпляре: два разных ответа об одном адресе — расхождение,
которое нельзя предотвратить, пока правило записано дважды.
"""

from __future__ import annotations

from typing import Callable, Iterable

from habitus.clean.geocode import geocode_address
from habitus.online.schema import HouseholdLegIntent, ParsedQuery

#: Суффикс города для геокодера: без него «офис» в питерском запросе
#: находится в Москве.
GEOCODE_CITY_NAME = {"msk": "Москва", "spb": "Санкт-Петербург"}
#: Метки, по которым считаем, что город уже назван в самом to_label — тогда
#: суффикс не добавляется (иначе «Москва Сити, Москва»).
GEOCODE_CITY_HINTS = {"msk": ("моск",), "spb": ("петербург", "спб", "питер")}
MSK_BOUNDS = (37.30, 55.48, 37.95, 55.95)


def inside_moscow(point: tuple[float, float]) -> bool:
    lon, lat = point
    west, south, east, north = MSK_BOUNDS
    return west <= lon <= east and south <= lat <= north


def geocode_leg(intent: HouseholdLegIntent, city: str,
                geocoder: Callable[[str], tuple[float, float] | None] = geocode_address,
                ) -> tuple[float, float] | None:
    """Точка назначения одной поездки. None — геокодер не нашёл или нашёл
    заведомо не то (адрес за пределами города при немаршрутном-по-метро
    режиме). Для метро границей служит сам граф: он шире города, МЦД уходят
    в область.
    """
    city_name = GEOCODE_CITY_NAME.get(city, "Москва")
    hints = GEOCODE_CITY_HINTS.get(city, ("моск",))
    label_lower = intent.to_label.lower()
    target = geocoder(intent.to_label if any(h in label_lower for h in hints)
                      else f"{intent.to_label}, {city_name}")
    if target is None:
        return None
    if intent.mode != "metro" and city == "msk" and not inside_moscow(target):
        return None
    return target


def household_points(pq: ParsedQuery, city: str,
                     geocoder: Callable[[str], tuple[float, float] | None] = geocode_address,
                     ) -> list[tuple[float, float]]:
    """Все точки, которые семья реально называет, без дублей и без выдумок.

    Поездка, чью цель геокодер не нашёл, в список не попадает: место, которого
    мы не нашли, не может влиять на порядок выдачи — иначе ранжирование
    опиралось бы на догадку о том, где эта цель находится.
    """
    seen: set[tuple[float, float]] = set()
    points: list[tuple[float, float]] = []
    for member in pq.household:
        for leg in member.legs:
            target = geocode_leg(leg, city, geocoder)
            if target is None or target in seen:
                continue
            seen.add(target)
            points.append(target)
    return points


def geodesic_metres(a: tuple[float, float], b: tuple[float, float]) -> float:
    """Гаверсинус по [lng, lat]."""
    import math

    r = 6371000.0
    lon1, lat1 = math.radians(a[0]), math.radians(a[1])
    lon2, lat2 = math.radians(b[0]), math.radians(b[1])
    dlon, dlat = lon2 - lon1, lat2 - lat1
    h = (math.sin(dlat / 2) ** 2
         + math.cos(lat1) * math.cos(lat2) * math.sin(dlon / 2) ** 2)
    return 2 * r * math.asin(min(1.0, math.sqrt(h)))


def total_metres(home: tuple[float, float],
                 points: Iterable[tuple[float, float]]) -> float:
    """Суммарная удалённость дома от всех названных точек.

    Сумма, а не среднее и не максимум: она и есть «сколько семья суммарно
    ездит», а именно это домохозяйство и минимизирует, выбирая квартиру.
    """
    return sum(geodesic_metres(home, p) for p in points)
