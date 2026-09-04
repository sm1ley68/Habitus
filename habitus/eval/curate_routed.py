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
    минуты. Пешие плечи до станции и от неё считаются по прямой (walker=None,
    см. ниже) — на квоту публичного ORS пересборка эталона не влезает.
  * Кандидат, до которого граф не доехал, из эталона выбывает — приписывать
    ему штрафное время значит выдумывать замер.

Запуск:  uv run python -m habitus.eval.curate_routed
Результат: habitus/eval/queries-routed.yaml — только d-серия, тот же формат.
"""
import sys
from pathlib import Path

import psycopg
import yaml

from habitus.config import settings
from habitus.eval.curate import GOLDEN, TOP_N, eligible_rows, grade, score
from habitus.online.geo import ORSProvider
from habitus.online.household import household_cost
from habitus.geo.metro_access import ORSWalker
from habitus.online.metro_route import door_to_door

OUT = Path(__file__).parent / "queries-routed.yaml"

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


def main() -> int:
    with open(GOLDEN, encoding="utf-8") as f:
        items = yaml.safe_load(f)

    # Пешие плечи считаются по прямой намеренно, walker=None. Через ORS каждая
    # поездка стоит несколько запросов на подбор ближайших станций у обоих
    # концов, и пересборка эталона (40 кандидатов x 5 запросов x 2 конца)
    # вылезает за суточную квоту публичного ключа в тысячи вызовов — причём
    # плечо «дом -> станция» это 5-10 минут, а решает порядок рельсовая часть.
    # На своём инстансе ORS (README, «Свой OpenRouteService») ограничение
    # снимается: тогда сюда стоит передать ORSWalker и пересобрать эталон.
    walker = None

    out = []
    with psycopg.connect(settings.db_dsn) as conn:
        for item in items:
            points = [(float(p[0]), float(p[1]))
                      for p in (item.get("household_points") or [])]
            if not points:
                continue
            exp = item.get("expected_parse") or {}
            rows = eligible_rows(conn, exp, item.get("match"))
            # Шортлист по прямой — вход маршрутизации, не эталон.
            rows.sort(key=lambda r: (household_cost((r["lon"], r["lat"]), points),
                                     r["external_id"]))
            shortlist = rows[:SHORTLIST_N]

            costed = []
            for r in shortlist:
                cost = routed_cost(conn, "msk",
                                   (r["lon"], r["lat"]), points, walker)
                if cost is not None:
                    costed.append((cost, r["external_id"]))
            costed.sort()
            ids = [eid for _, eid in costed[:TOP_N]]
            straight = [r["external_id"] for r in shortlist[:TOP_N]]
            overlap = len(set(ids) & set(straight))
            print(f"{item['id']}: маршрутизировано {len(costed)}/{len(shortlist)}, "
                  f"пересечение с эталоном по прямой {overlap}/10")
            out.append({**item,
                        "relevant_ids": ids,
                        "relevance": {eid: grade(i, len(ids))
                                      for i, eid in enumerate(ids)}})

    with open(OUT, "w", encoding="utf-8") as f:
        yaml.safe_dump(out, f, allow_unicode=True, sort_keys=False)
    print(f"записано: {OUT} ({len(out)} запросов)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
