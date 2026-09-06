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

from typing import Callable, Iterable, Literal

from habitus.clean.geocode import geocode_address
from habitus.online.schema import (GeoConstraint, HouseholdLegIntent,
                                   ParsedQuery)

#: Суффикс города для геокодера: без него «офис» в питерском запросе
#: находится в Москве.
GEOCODE_CITY_NAME = {"msk": "Москва", "spb": "Санкт-Петербург"}
#: Метки, по которым считаем, что город уже назван в самом to_label — тогда
#: суффикс не добавляется (иначе «Москва Сити, Москва»).
GEOCODE_CITY_HINTS = {"msk": ("моск",), "spb": ("петербург", "спб", "питер")}
MSK_BOUNDS = (37.30, 55.48, 37.95, 55.95)

#: Метки-категории, которые НЕЛЬЗЯ геокодировать. «Школа» — это не место, а
#: класс мест: Nominatim на такой запрос возвращает произвольную школу города,
#: и дальше она едет в ранжирование и в досье как настоящий факт. Именно так
#: появлялось «ребёнку до школы 156 минут»: запрос «ребёнку в школу пешком» не
#: называл школу, геокодер выдал случайную в Кузьминках, и объект в Переделкине
#: оказался «удачным» относительно неё. Выдуманная точка хуже отсутствующей:
#: отсутствие видно и оговаривается в заметке, а выдумка выглядит как замер.
#:
#: Проверяется по ОЧИЩЕННОЙ метке (без города и служебных слов): «Лицей 239» и
#: «школа №57» — настоящие названия и геокодируются, «школа» и «работа» — нет.
GENERIC_LABELS = {
    "школа", "школы", "садик", "детский сад", "сад", "вуз", "университет",
    "институт", "работа", "офис", "метро", "парк", "магазин", "поликлиника",
    "больница", "спортзал", "зал", "секция", "кружок",
    # Выгул собаки — тот же класс мест, что «школа», и та же ловушка: на
    # «зону выгула» геокодер отдаёт произвольную площадку города, и район
    # подбирается вокруг чужого двора. Меряется выгул колонкой walk_min_park
    # (парк и зона выгула для подбора района — одно и то же место), поэтому
    # NLU велено присылать «парк»; остальные написания держим сеткой
    # безопасности, а не вторым правилом.
    "выгул", "выгул собаки", "выгул собак", "зона выгула", "зона выгула собак",
    "площадка для выгула", "площадка для выгула собак", "собачья площадка",
    "сквер", "ветклиника", "ветеринарная клиника", "ветеринар",
}


#: Разговорные названия, на которых геокодер уверенно ошибается. «Сити,
#: Москва» Nominatim отдаёт точку на севере города (37.637, 55.804) вместо
#: делового центра (37.539, 55.749) — и человек, попросивший «на работу в
#: Сити», получает подбор вокруг чужого места. Список короткий и держится
#: только для случаев, проверенных руками: угадывать за пользователя нельзя,
#: но и молча возвращать заведомо неверную точку тоже.
LANDMARK_ALIAS = {
    "сити": "Москва-Сити",
    "сити, москва": "Москва-Сити",
    "москва сити": "Москва-Сити",
    "деловой центр": "Москва-Сити",
}


def is_generic_label(label: str) -> bool:
    """Метка называет класс мест, а не место."""
    cleaned = label.lower().replace("ё", "е")
    for suffix in (", москва", ", санкт-петербург", ", спб", ", питер"):
        cleaned = cleaned.replace(suffix, "")
    cleaned = cleaned.strip(" .,;:")
    return cleaned in {g.replace("ё", "е") for g in GENERIC_LABELS}


def inside_moscow(point: tuple[float, float]) -> bool:
    lon, lat = point
    west, south, east, north = MSK_BOUNDS
    return west <= lon <= east and south <= lat <= north


#: Порог пешей доступности, когда человек сказал «пешком», но минут не назвал.
#: 15 — не догадка о его желаниях, а осознанная точка старта: продукт умеет
#: ослаблять гео-порог шагами по 5 минут до потолка 30 (GEO_STEP_MIN и
#: GEO_CAP_MIN в orchestrator), и 15 оставляет три шага запаса, если окажется
#: строго. Число ОБЯЗАНО быть названо пользователю в заметке: принятое за него
#: решение, о котором он не знает, — тот же выдуманный факт.
DEFAULT_WALK_MINUTES = 15

#: Категории, для которых у объявления есть ИЗМЕРЕННАЯ пешая доступность
#: (колонки walk_min_*). Остальные категории требованием к району стать не
#: могут: мерить нечем.
DISTRICT_KINDS = ("school", "park", "metro")


#: Категории, у которых в базе есть слой POI с координатами и названиями.
#: Из этих же точек посчитаны колонки walk_min_* — то есть ближайший объект
#: не выдумка, а ровно та величина, которой продукт уже меряет доступность.
POI_KINDS = {"school": "школа", "park": "парк", "metro": "метро"}


def nearest_poi(conn, city: str, kind: str, home: tuple[float, float]
                ) -> tuple[tuple[float, float], str] | None:
    """Ближайший объект категории к дому: (координата, название).

    Нужен там, где человек назвал КЛАСС мест, а не место: «ребёнку в школу
    пешком». Точку конкретной школы выдумывать нельзя, но ближайшая школа —
    измеренный факт о доме, и показать маршрут до неё честно, если подписать
    её именно ближайшей, а не «той, которую вы имели в виду».

    None — слоя нет или в нём пусто; тогда нога остаётся непоказанной.
    """
    row = conn.execute(
        "SELECT ST_X(geom), ST_Y(geom), name FROM poi "
        "WHERE city = %s AND kind = %s AND geom IS NOT NULL "
        "ORDER BY geom <-> ST_SetSRID(ST_MakePoint(%s, %s), 4326) LIMIT 1;",
        (city, kind, home[0], home[1])).fetchone()
    if row is None:
        return None
    lon, lat, name = row
    return (float(lon), float(lat)), (name or POI_KINDS.get(kind, kind))


#: Чем нога домохозяйства оказывается для поиска. Собака ничего нового сюда не
#: добавила: «выгул» — это нога члена домохозяйства к КЛАССУ мест, ровно как
#: «ребёнку в школу пешком», и роль у неё та же самая.
LegRole = Literal["point", "district", "unmeasured"]


def leg_role(leg: HouseholdLegIntent) -> LegRole:
    """Единственное место, где решается, чем нога станет для поиска.

    - "point" — место названо ИМЕНЕМ («Лицей 239»): его можно геокодировать,
      и оно работает сигналом ранжирования (household_points).
    - "district" — назван КЛАСС мест, пешая доступность которого измерена
      колонкой walk_min_* («парк», «школа»): становится требованием к району.
    - "unmeasured" — назван класс мест без измеренной колонки
      («поликлиника»), либо поездка не пешая: не становится ничем.

    Раньше это правило было размазано по трём местам — district_requirements,
    _family_data и счётчику заметок в pipeline — и они успели разойтись:
    pipeline считал ногу-класс «местом, которое не удалось найти на карте»,
    хотя именно она и отфильтровала выдачу.
    """
    if not is_generic_label(leg.to_label):
        return "point"
    if leg.mode == "walk" and leg.to_kind in DISTRICT_KINDS:
        return "district"
    return "unmeasured"


def district_requirements(pq: ParsedQuery) -> list[GeoConstraint]:
    """Обобщённые метки поездок → требования к району.

    «Ребёнку в школу пешком» не называет школу, поэтому точкой на карте стать
    не может (см. GENERIC_LABELS). Но у продукта для каждого объявления уже
    посчитано walk_min_school — то есть требование выразимо честно, измеренными
    данными: не «до ЭТОЙ школы», а «школа в пешей доступности». «Живём с
    собакой» проходит здесь тем же путём: выгул — это walk_min_park.

    Не перебивает то, что человек сказал явно: если он назвал минуты и NLU
    положил их в pq.geo, оттуда и берём. Возвращает только НОВЫЕ ограничения,
    вызывающий добавляет их к существующим.
    """
    already = {g.kind for g in pq.geo}
    out: list[GeoConstraint] = []
    for member in pq.household or []:
        for leg in member.legs:
            if leg_role(leg) != "district" or leg.to_kind in already:
                continue
            already.add(leg.to_kind)
            out.append(GeoConstraint(kind=leg.to_kind,
                                     walk_minutes=DEFAULT_WALK_MINUTES))
    return out


def household_notes(pq: ParsedQuery,
                    points: list[tuple[float, float]]) -> list[str]:
    """Что честно сказать человеку про названный им состав домохозяйства.

    Заметку про принятый порог пешей доступности выдаёт вызывающий рядом с
    district_requirements — она про ограничение, а не про ногу. Здесь остаются
    две вещи, о которых промолчать нельзя: сколько НАЗВАННЫХ мест реально
    доехало до ранжирования, и какие классы мест продукт мерить не умеет.
    """
    roles = [(leg, leg_role(leg)) for m in pq.household for leg in m.legs]
    named = sum(1 for _, role in roles if role == "point")
    notes: list[str] = []
    if points:
        notes.append(
            f"учли {len(points)} из {named} названных мест семьи как "
            f"предпочтение по расположению — это близость по прямой, "
            f"время в пути считается в досье объекта")
    elif named:
        # Молчать нельзя: пользователь назвал места и вправе знать, что на
        # выдачу они не повлияли.
        notes.append(
            f"места семьи ({named}) не удалось найти на карте — на порядок "
            f"выдачи они не повлияли")
    # Класс мест без измеренной колонки исчезает бесследно: ни точкой, ни
    # требованием он стать не может. Об этом тоже нельзя молчать — иначе
    # человек считает, что его поликлинику учли.
    for label in dict.fromkeys(leg.to_label for leg, role in roles
                               if role == "unmeasured"):
        notes.append(
            f"«{label}» — это класс мест, а не адрес: пешую доступность "
            f"таких мест продукт пока не меряет, на выдачу это не повлияло")
    return notes


def geocode_leg(intent: HouseholdLegIntent, city: str,
                geocoder: Callable[[str], tuple[float, float] | None] = geocode_address,
                ) -> tuple[float, float] | None:
    """Точка назначения одной поездки. None — геокодер не нашёл или нашёл
    заведомо не то (адрес за пределами города при немаршрутном-по-метро
    режиме). Для метро границей служит сам граф: он шире города, МЦД уходят
    в область.
    """
    label = LANDMARK_ALIAS.get(intent.to_label.lower().strip(" .,;:"), intent.to_label)
    intent = intent.model_copy(update={"to_label": label})
    if is_generic_label(intent.to_label):
        # Не геокодируем класс мест: см. GENERIC_LABELS. Нога выпадает, и
        # вызывающий честно сообщает, сколько мест из названных учтено.
        return None
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
    """Суммарная удалённость дома от всех названных точек."""
    return sum(geodesic_metres(home, p) for p in points)


def household_cost(home: tuple[float, float],
                   points: Iterable[tuple[float, float]]) -> float:
    """Во что обходится семье это расположение: среднее плечо + худшее плечо.

    Одной суммой (или одним средним) мерить нельзя: на отрезке между двумя
    офисами сумма расстояний ПОСТОЯННА, поэтому «жить вплотную к офису мужа, а
    жене ездить через весь город» и «жить посередине» получают одинаковый балл.
    Семья, которая просит «компромисс между Сколково и Сити», просит ровно
    обратного.

    Поэтому к среднему добавляется максимум — то самое худшее плечо, которым
    досье оценивает блок «Суточный ритм семьи» (_routing_grade): именно самая
    долгая поездка ограничивает расписание, а среднее её маскирует. Сумма
    остаётся внутри среднего, так что общий объём разъездов тоже учтён.

    Пустой список точек — 0.0: считать нечего, и это не «идеально близко»,
    а отсутствие сигнала; вызывающий обязан проверять непустоту сам.
    """
    distances = [geodesic_metres(home, p) for p in points]
    if not distances:
        return 0.0
    return sum(distances) / len(distances) + max(distances)
