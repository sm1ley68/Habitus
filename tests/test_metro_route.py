import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro import LineRaw, StationRaw, upsert_transit
from habitus.geo.metro_times import CuratedTimes
from habitus.online.metro_route import (
    MetroGraph,
    Station,
    clear_graph_cache,
    load_graph,
)

#  линия 1:  A(1) — B(2) — C(3)      по 100 с
#  линия 2:  B'(4) — D(5)            120 с
#  пересадка B(2) ↔ B'(4): 180 с, headway линии 2 = 600 с
STATIONS = {
    1: Station(1, "A", "1", "линия 1", "subway", "#f00", 37.60, 55.75),
    2: Station(2, "B", "1", "линия 1", "subway", "#f00", 37.62, 55.75),
    3: Station(3, "C", "1", "линия 1", "subway", "#f00", 37.64, 55.75),
    4: Station(4, "B", "2", "линия 2", "mcd", "#00f", 37.62, 55.75),
    5: Station(5, "D", "2", "линия 2", "mcd", "#00f", 37.62, 55.78),
}
EDGES = {
    (1, 2): (100, False), (2, 1): (100, False),
    (2, 3): (100, True),  (3, 2): (100, True),
    (4, 5): (120, False), (5, 4): (120, False),
}
TRANSFERS = {(2, 4): (180, False, True), (4, 2): (180, False, True)}
# R29/R30: ключ — (system, ref), не голый ref (metro_line уникален по
# (city, system, ref); одинаковый ref в разных системах не должен
# схлопывать интервалы в один). Значение — (секунды, оценка ли).
HEADWAYS = {("subway", "1"): (120, False), ("mcd", "2"): (600, False)}


@pytest.fixture
def graph() -> MetroGraph:
    return MetroGraph(stations=STATIONS, edges=EDGES, transfers=TRANSFERS,
                      headways=HEADWAYS)


def test_direct_ride_on_one_line(graph):
    r = graph.route({1: 0}, {3: 0})
    assert len(r.segments) == 1 and not r.transfers
    seg = r.segments[0]
    assert (seg.from_station, seg.to_station, seg.stops) == ("A", "C", 2)
    # 200 с езды + интервал линии 1 на посадку
    assert r.ride_seconds == 200 + HEADWAYS[("subway", "1")][0]


def test_estimated_edge_taints_the_whole_route(graph):
    # честность важнее оптимизма: маршрут с оценочным перегоном — оценочный
    assert graph.route({1: 0}, {3: 0}).estimated is True
    assert graph.route({1: 0}, {2: 0}).estimated is False


def test_transfer_costs_walk_plus_new_line_headway(graph):
    r = graph.route({1: 0}, {5: 0})
    assert len(r.segments) == 2 and len(r.transfers) == 1
    assert r.transfers[0].outdoor is True
    # 100 (A→B) + 120 (посадка на 1) + 180 (переход) + 600 (интервал 2) + 120 (B'→D)
    assert r.ride_seconds == 100 + 120 + 180 + 600 + 120


def test_segments_carry_line_identity_for_rendering(graph):
    r = graph.route({1: 0}, {5: 0})
    assert [s.system for s in r.segments] == ["subway", "mcd"]
    assert [s.line_ref for s in r.segments] == ["1", "2"]
    assert r.segments[1].colour == "#00f"


def test_seed_walk_seconds_choose_the_better_entrance(graph):
    # вход через A дороже пешком, но через C ехать некуда — берётся A
    assert graph.route({1: 60, 3: 900}, {2: 0}).segments[0].from_station == "A"


def test_target_walk_seconds_are_included_in_choice(graph):
    fast = graph.route({1: 0}, {2: 0}).ride_seconds
    slow = graph.route({1: 0}, {2: 300}).ride_seconds
    assert slow == fast + 300


def test_times_from_is_one_pass_to_all(graph):
    times = graph.times_from({1: 0})
    assert times[1] == 0
    assert times[2] == 100 + HEADWAYS[("subway", "1")][0]
    assert 5 in times, "через пересадку станция должна быть достижима"


def test_times_from_honours_multiple_seeds(graph):
    # два входа с разными пешими плечами: минимум берётся сам, без сравнений снаружи
    times = graph.times_from({1: 0, 3: 10})
    h1 = HEADWAYS[("subway", "1")][0]
    assert times[2] == min(100 + h1, 10 + 100 + h1)


def test_unreachable_returns_none(graph):
    lonely = MetroGraph(stations={9: STATIONS[1]}, edges={}, transfers={},
                        headways=HEADWAYS)
    assert lonely.route({9: 0}, {1: 0}) is None


def test_empty_seeds_or_targets_return_none(graph):
    assert graph.route({}, {3: 0}) is None
    assert graph.route({1: 0}, {}) is None


# --- R29/R30: нет дефолта 0 для отсутствующего интервала --------------------

def test_missing_headway_entry_raises_instead_of_defaulting_to_zero():
    # Синтетический ноль вместо отсутствующего замера запрещён (CLAUDE.md).
    # Станция на линии, которой нет в headways — это баг построения графа
    # (Задача 6 не заполнила headway), и он обязан всплыть исключением, а
    # не молча показать пользователю "ждать не придётся".
    broken = MetroGraph(stations=STATIONS, edges=EDGES, transfers=TRANSFERS,
                        headways={})
    with pytest.raises(KeyError):
        broken.route({1: 0}, {3: 0})
    with pytest.raises(KeyError):
        broken.times_from({1: 0})


def test_headway_key_is_scoped_by_system_not_just_ref():
    # Одинаковый ref ("1") в двух РАЗНЫХ системах не должен схлопнуться в
    # один интервал — ключ обязан включать system (Задача 9, R29/R30).
    stations = {
        1: Station(1, "A", "1", "линия 1", "subway", "#f00", 37.60, 55.75),
        2: Station(2, "B", "1", "линия 1", "subway", "#f00", 37.62, 55.75),
        10: Station(10, "X", "1", "линия X", "mcd", "#0f0", 37.60, 55.80),
        11: Station(11, "Y", "1", "линия X", "mcd", "#0f0", 37.62, 55.80),
    }
    edges = {(1, 2): (100, False), (2, 1): (100, False),
             (10, 11): (300, False), (11, 10): (300, False)}
    headways = {("subway", "1"): (50, False), ("mcd", "1"): (900, False)}
    g = MetroGraph(stations=stations, edges=edges, transfers={},
                   headways=headways)
    # subway/1 использует 50, а не то, что лежит под mcd/1 (900) — если бы
    # ключ был голым ref, оба маршрута схлопнулись бы на одно значение.
    assert g.route({1: 0}, {2: 0}).ride_seconds == 100 + 50
    assert g.route({10: 0}, {11: 0}).ride_seconds == 300 + 900


def test_route_estimated_flag_ors_in_headway_estimated():
    # Оценка интервала линии, на которую садимся, тоже красит маршрут как
    # estimated — даже если все рёбра и переход измерены точно.
    headways_est = {("subway", "1"): (120, False), ("mcd", "2"): (600, True)}
    g = MetroGraph(stations=STATIONS, edges=EDGES, transfers=TRANSFERS,
                   headways=headways_est)
    r = g.route({1: 0}, {5: 0})
    assert r.estimated is True, "headway_estimated линии 2 обязан протечь в route.estimated"

    r2 = g.route({1: 0}, {2: 0})
    assert r2.estimated is False, "здесь используется только линия 1 (не оценка)"


def test_seed_line_headway_estimated_taints_route():
    # То же самое, но оценка сидит на линии ПОСЕВА, а не на линии пересадки.
    headways_est = {("subway", "1"): (120, True), ("mcd", "2"): (600, False)}
    g = MetroGraph(stations=STATIONS, edges=EDGES, transfers=TRANSFERS,
                   headways=headways_est)
    assert g.route({1: 0}, {2: 0}).estimated is True


# --- R4: срез фактического пути, а не поиск соседа по line_ref -------------

def test_ring_line_path_slicing_picks_the_actual_shorter_side():
    # Кольцо A-B-C-D-A. Путь через B короче пути через D — правильная
    # реализация обязана взять именно его и не зациклиться на кольце.
    ring_stations = {
        10: Station(10, "A", "R", "кольцо", "mck", "#0ff", 37.50, 55.70),
        11: Station(11, "B", "R", "кольцо", "mck", "#0ff", 37.52, 55.70),
        12: Station(12, "C", "R", "кольцо", "mck", "#0ff", 37.52, 55.72),
        13: Station(13, "D", "R", "кольцо", "mck", "#0ff", 37.50, 55.72),
    }
    ring_edges = {
        (10, 11): (5, False), (11, 10): (5, False),
        (11, 12): (5, False), (12, 11): (5, False),
        (12, 13): (50, False), (13, 12): (50, False),
        (13, 10): (50, False), (10, 13): (50, False),  # замыкающее ребро
    }
    g = MetroGraph(stations=ring_stations, edges=ring_edges, transfers={},
                   headways={("mck", "R"): (60, False)})
    r = g.route({10: 0}, {12: 0})
    assert len(r.segments) == 1
    seg = r.segments[0]
    assert (seg.from_station, seg.to_station, seg.stops) == ("A", "C", 2)
    assert seg.seconds == 10               # 5 + 5, не 5 + 50 и не 50 + 50
    assert r.ride_seconds == 10 + 60       # + интервал посадки


# --- load_graph / clear_graph_cache: чтение графа из БД --------------------

def _line(ref="1", system="subway", names=("A", "B", "C"), lon0=37.60):
    return LineRaw(
        system=system, ref=ref, name=f"линия {ref}", colour="#EF161E",
        stations=[StationRaw(osm_id=1000 + i, name=n, lon=lon0 + i * 0.02,
                             lat=55.75)
                  for i, n in enumerate(names)],
        geometry=[[lon0 + i * 0.02, 55.75] for i in range(len(names))],
        ring=False)


def _curated():
    c = CuratedTimes()
    c.headways = {"1": 120}
    c.speeds = {"1": 40.0}
    c.edges = {("1", "a", "b"): 150, ("1", "b", "c"): 150}
    return c


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        c.commit()
        clear_graph_cache()
        yield c
        clear_graph_cache()


def test_load_graph_returns_none_for_unknown_city(conn):
    assert load_graph(conn, "nonexistent-city") is None


def test_load_graph_roundtrips_a_route(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    g = load_graph(conn, "msk")
    assert g is not None
    r = g.route(
        {s.id: 0 for s in g.stations.values() if s.name == "A"},
        {s.id: 0 for s in g.stations.values() if s.name == "C"})
    assert r is not None
    assert r.segments[0].line_ref == "1"
    assert r.segments[0].colour == "#EF161E"


def test_load_graph_cache_invalidates_after_rebuild(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    g1 = load_graph(conn, "msk")
    g2 = load_graph(conn, "msk")
    assert g1 is g2, "тот же отпечаток — тот же объект из кэша"

    with conn.cursor() as cur:
        cur.execute("TRUNCATE metro_line CASCADE;")
    conn.commit()
    upsert_transit([_line("2", names=("X", "Y"))], conn, "msk", _curated())
    g3 = load_graph(conn, "msk")
    assert g3 is not g1, "перестройка графа обязана дать новый отпечаток"
    assert set(s.line_ref for s in g3.stations.values()) == {"2"}
