# habitus/eval/curate_routed.py — эталон d-серии на ВРЕМЕНИ в пути, а не на расстоянии.
"""Независимый эталон для сценариев домохозяйства.

Зачем отдельный файл эталона. Канал retrieval упорядочивает выборку той же
метрикой `household_cost` (расстояние по прямой), которой построен эталон
d-серии в queries.yaml. Поэтому её метрики нельзя считать подтверждением
качества ранжирования — и, главное, по ней нельзя подбирать
`settings.household_weight`: подбор свёлся бы к «сделай сигнал сильнее, и
совпадение с эталоном вырастет». Оговорка записана в
docs/notes/eval-baseline-2026-09-04.md.

Здесь эталон строится по ДРУГОМУ основанию — времени от двери до двери по
графу метро (`door_to_door`), с пешими плечами по дорожной сети через ORS.
Это не пересчёт расстояния в минуты: топология линий, пересадки и переправы
через реку меняют порядок так, как расстояние по прямой не умеет — «через
дорогу, но по другой ветке» дороже, чем «дальше, но по прямой ветке».

Честные ограничения, которые обязан знать читатель метрик:

  * Маршрутизируется не весь пул, а SHORTLIST_N лучших по прямой. Гонять
    ORS/Дейкстру по тысячам кандидатов нереально, поэтому эталон — это
    ПЕРЕУПОРЯДОЧЕНИЕ короткого списка, а не независимый отбор с нуля. Объект,
    далёкий по прямой, но быстрый по метро, в шортлист не попадёт.
  * Времена перегонов в графе метро сами модельные (в metro_edge estimated
    почти у всех рёбер), так что независима здесь ТОПОЛОГИЯ, а не абсолютные
    минуты. Пешие плечи до станции и от неё считаются по дорожной сети, когда
    настроен ORS (на публичном ключе это не влезало в квоту — см. ниже).
  * Кандидат, до которого граф не доехал, из эталона выбывает — приписывать
    ему штрафное время значит выдумывать замер.

Модуль-библиотека: канонический эталон строит `habitus.eval.curate`, который
зовёт отсюда `routed_reference` для запросов с точками семьи. Отдельного файла
эталона нет намеренно — два источника правды разошлись бы, и следующий прогон
curate молча вернул бы воздушную метрику.
"""

import psycopg

from habitus.config import settings
from habitus.eval.curate import TOP_N, eligible_rows, grade
from habitus.online.geo import ORSProvider
from habitus.online.household import household_cost
from habitus.geo.metro_access import ORSWalker
from habitus.online.metro_route import door_to_door

#: Сколько лучших по прямой уезжает в маршрутизацию. Широкая сеть здесь важнее
#: экономии: эталон — это ПЕРЕУПОРЯДОЧИВАНИЕ шортлиста, и чем он уже, тем
#: сильнее эталон наследует ту самую метрику по прямой, от которой мы и хотим
#: уйти. Маршрутизация локальная (граф метро, без ORS), 300 кандидатов на
#: запрос считаются секунды, так что платить за узкий список нечем.
SHORTLIST_N = 300


def routed_cost(conn, city: str, home: tuple[float, float],
                points: list[tuple[float, float]], walker) -> float | None:
    """Цена расположения в минутах: среднее плечо + худшее.

    Форма агрегата ровно та же, что у household_cost, — меняется только сама
    величина плеча (минуты по графу вместо метров по прямой). Иначе сравнение
    двух эталонов мерило бы разницу агрегатов, а не разницу метрик.
    """
    legs = []
    for p in points:
        got = door_to_door(conn, city, home, p, walker=walker)
        if got is None:
            return None
        legs.append(got[0].total_minutes)
    return sum(legs) / len(legs) + max(legs)


def routed_reference(conn, item: dict, walker, *, verbose: bool = False
                     ) -> tuple[list[str], dict[str, int]] | None:
    """Эталон одного запроса по времени в пути. None — точек семьи в нём нет.

    Зовётся и из curate: канонический эталон для сценариев домохозяйства
    строится ЗДЕСЬ, иначе следующий прогон curate вернул бы воздушную метрику
    обратно и молча отменил бы решение о смене основания.
    """
    points = [(float(p[0]), float(p[1]))
              for p in (item.get("household_points") or [])]
    if not points:
        return None
    exp = item.get("expected_parse") or {}
    rows = eligible_rows(conn, exp, item.get("match"))
    # Шортлист по прямой — вход маршрутизации, не эталон.
    rows.sort(key=lambda r: (household_cost((r["lon"], r["lat"]), points),
                             r["external_id"]))
    shortlist = rows[:SHORTLIST_N]
    costed = []
    for r in shortlist:
        cost = routed_cost(conn, "msk", (r["lon"], r["lat"]), points, walker)
        if cost is not None:
            costed.append((cost, r["external_id"]))
    costed.sort()
    ids = [eid for _, eid in costed[:TOP_N]]
    if verbose:
        straight = {r["external_id"] for r in shortlist[:TOP_N]}
        print(f"{item['id']}: маршрутизировано {len(costed)}/{len(shortlist)}, "
              f"пересечение с эталоном по прямой {len(set(ids) & straight)}/10")
    return ids, {eid: grade(i, len(ids)) for i, eid in enumerate(ids)}
