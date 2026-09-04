"""Стоимость расположения для семьи, измеренная ВРЕМЕНЕМ, а не воздухом.

Канал retrieval, который учитывает места семьи, до сих пор упорядочивал
выборку по `household_cost` — среднему плюс худшему расстоянию ПО ПРЯМОЙ.
Замер против эталона по времени в пути показал цену этой подмены: из десяти
эталонных объектов retrieval находил один-три на трёх запросах d-серии из
пяти (docs/notes/eval-baseline-2026-09-04.md). Квартира на той же ветке без
пересадок, но далёкая по воздуху, в сотню кандидатов не попадала вовсе — а
переупорядочить можно только найденное, так что ни вес в бленде, ни голова в
срезе пула этого исправить не могли.

Здесь то же самое считается временем от двери до двери по графу метро.
Механика ровно та, что уже работает в `metro_predicate_with_note`: Дейкстра
из станций ЦЕЛИ (один обход на точку переиспользуется для всех объявлений
сразу), результат уезжает в SQL VALUES-джойном по `listing_metro_access`.
Второй формулы времени не заводится — берётся тот же `graph.times_from`,
которым считается разбивка маршрута в досье.

Форма агрегата не меняется: среднее плечо + худшее. Это та же величина, что
у `household_cost` и у `_routing_grade` в досье, — меняется только измерение
плеча. Иначе сравнение старого и нового канала мерило бы разницу агрегатов.

Деградация честная: если графа нет, у точки нет платформ в пешей доступности
или пеших плеч по городу не рассчитано, функция возвращает None, и
вызывающий откатывается на расстояние по прямой — с заметкой, а не молча.
"""
from __future__ import annotations

from typing import Sequence

import psycopg

from habitus.online.metro_route import load_graph, nearest_stations


def point_times(conn: psycopg.Connection, city: str,
                point: tuple[float, float]) -> dict[int, int] | None:
    """«id платформы → секунды до этой точки». None — считать нечем."""
    graph = load_graph(conn, city)
    if graph is None:
        return None
    targets = nearest_stations(conn, city, point[0], point[1])
    if not targets:
        return None
    return graph.times_from(targets) or None


def all_point_times(conn: psycopg.Connection, city: str,
                    points: Sequence[tuple[float, float]],
                    ) -> list[dict[int, int]] | None:
    """Времена по КАЖДОЙ точке семьи. None — хотя бы одна точка непосчитана.

    Именно все, а не сколько получилось: цена расположения — это среднее и
    худшее по всем названным местам, и молча выкинуть одно из них значит
    подменить запрос семьи другим запросом.
    """
    out = []
    for p in points:
        times = point_times(conn, city, p)
        if times is None:
            return None
        out.append(times)
    return out


def channel_sql(times_per_point: list[dict[int, int]], where: str,
                where_params: Sequence) -> tuple[str, list]:
    """Запрос канала: id объявлений по возрастанию времени, дешёвые сверху.

    Двухуровневая агрегация: внутри — минимум по платформам объявления для
    каждой точки (каким входом в метро дешевле), снаружи — среднее плюс
    худшее по точкам. Одним уровнем это не считается.

    HAVING count(*) = число точек: объявление, до которого хоть одна точка
    семьи по графу недостижима, из канала выбывает. Приписать ему штрафное
    время значит выдумать замер, а посчитать среднее по доступным точкам —
    подменить «не знаем» на «близко».
    """
    n = len(times_per_point)
    values, params = [], []
    for idx, times in enumerate(times_per_point):
        for station_id, seconds in times.items():
            values.append("(%s::bigint,%s::int,%s::int)")
            params.extend([station_id, seconds, idx])
    if not values:
        return "", []
    sql = (
        f"SELECT external_id FROM ("
        f"  SELECT a.external_id, t.p, min(a.walk_seconds + t.seconds) AS leg"
        f"  FROM listing_metro_access a"
        f"  JOIN (VALUES {','.join(values)}) AS t(station_id, seconds, p)"
        f"    ON t.station_id = a.station_id"
        f"  WHERE a.external_id IN (SELECT external_id FROM listings WHERE {where})"
        f"  GROUP BY a.external_id, t.p"
        f") legs GROUP BY external_id HAVING count(*) = {n} "
        f"ORDER BY (avg(leg) + max(leg)), external_id LIMIT %s;")
    return sql, params + list(where_params)


def costs(conn: psycopg.Connection, ext_ids: Sequence[str],
          times_per_point: list[dict[int, int]]) -> dict[str, float]:
    """Та же величина для конкретных кандидатов — зеркало channel_sql в Python.

    Нужна реранку: retrieval и бленд обязаны спорить об одном и том же. Объект
    без полного набора плеч в словарь не попадает — у вызывающего это значит
    «сигнала нет», а не «далеко».
    """
    if not ext_ids or not times_per_point:
        return {}
    rows = conn.execute(
        "SELECT external_id, station_id, walk_seconds FROM listing_metro_access "
        "WHERE external_id = ANY(%s);", (list(ext_ids),)).fetchall()
    access: dict[str, list[tuple[int, int]]] = {}
    for ext_id, station_id, walk in rows:
        access.setdefault(ext_id, []).append((station_id, walk))
    out: dict[str, float] = {}
    for ext_id, stations in access.items():
        legs = []
        for times in times_per_point:
            options = [walk + times[s] for s, walk in stations if s in times]
            if not options:
                break
            legs.append(min(options))
        if len(legs) == len(times_per_point):
            out[ext_id] = sum(legs) / len(legs) + max(legs)
    return out
