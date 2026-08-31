import math

import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro import LineRaw, StationRaw, upsert_transit
from habitus.geo.metro_times import CuratedTimes
from habitus.online.metro_route import (
    MAX_ENTRY_WALK_METRES,
    MetroGraph,
    Station,
    _nearest_stations_detailed,
    clear_graph_cache,
    door_to_door,
    load_graph,
    nearest_stations,
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


# --- R56 (фикс-раунд 1): route() и times_from() обязаны сходиться -----------

def test_route_and_times_from_agree_when_target_is_also_a_seed(graph):
    # Случай (a) из отчёта ревью: цель совпадает со входом — ехать не нужно
    # вовсе, ни headway, ни сегментов. До фикса здесь протекал фантомный
    # headway (120 с) без единого сегмента.
    r = graph.route({2: 0}, {2: 0})
    assert r.ride_seconds == 0
    assert r.segments == [] and r.transfers == []
    assert r.ride_seconds == graph.times_from({2: 0})[2] + 0


def test_times_from_keeps_the_cheaper_rail_path_over_a_pricier_seed_walk(graph):
    # Случай (b): станция 3 сама seed (пешее плечо 900 с), но рельсами от
    # станции 1 до неё дешевле (320 с — см. test_direct_ride_on_one_line) —
    # обязан победить рельсовый путь, а не безусловный walk.
    times = graph.times_from({1: 0, 3: 900})
    assert times[3] == 320


def test_route_reconciles_with_times_from_on_a_direct_ride(graph):
    r = graph.route({1: 0}, {3: 0})
    assert r.ride_seconds == graph.times_from({1: 0})[3] + 0


def test_route_reconciles_with_times_from_on_a_transfer(graph):
    r = graph.route({1: 0}, {5: 300})
    assert r.ride_seconds == graph.times_from({1: 0})[5] + 300


def test_route_reconciles_with_times_from_on_the_cheaper_rail_case(graph):
    # Тот же случай (b), но проверенный со стороны route(): выбирается
    # рельсовый путь 1→...→3 (320 с), а не тривиальный заход в 3 (900 с).
    seeds = {1: 0, 3: 900}
    r = graph.route(seeds, {3: 0})
    assert r.ride_seconds == 320
    assert r.segments, "рельсовый путь обязан иметь хотя бы один сегмент"
    assert r.ride_seconds == graph.times_from(seeds)[3] + 0


def test_route_and_times_from_agree_on_an_out_of_graph_seed(graph):
    # R59 (фикс-раунд 2): станция 77 не входит в self.stations вообще —
    # устаревший id в listing_metro_access после пересборки графа. До фикса
    # route({77:0},{77:0}) строила "тривиальный" маршрут (ride_seconds=0,
    # без сегментов) для станции, которую times_from вообще не знает.
    assert graph.times_from({77: 0}) == {}
    assert graph.route({77: 0}, {77: 0}) is None


def test_route_and_times_from_agree_on_a_mixed_out_of_graph_seed(graph):
    # Та же дыра, но семя из графа (1) подмешано к семени вне графа (77) —
    # цель 77 не должна тривиально "найтись" только потому, что она есть в
    # seeds; times_from тоже не должен знать о ней.
    times = graph.times_from({1: 0, 77: 30})
    assert 77 not in times
    assert graph.route({1: 0, 77: 30}, {77: 0}) is None


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


# --- R58 (фикс-раунд 1, минор): estimated на сегментах и переходах ---------

def test_segment_estimated_flag_reflects_its_edges(graph):
    # Отрезок A→C содержит перегон (2,3) с estimated=True в EDGES — сегмент
    # обязан унаследовать это, а не молчаливо остаться False. Удаление
    # накопления estimated в _segment оставляло бы этот тест единственным
    # красным местом среди всего набора.
    r = graph.route({1: 0}, {3: 0})
    assert r.segments[0].estimated is True


def test_transfer_estimated_flag_is_read_from_the_transfer_table():
    # Своя фикстура с estimated=True на самой пересадке (в общем graph все
    # TRANSFERS не оценочные) — проверяет, что Transfer.estimated идёт из
    # self.transfers, а не забыт по пути.
    transfers_est = {(2, 4): (180, True, True), (4, 2): (180, True, True)}
    g = MetroGraph(stations=STATIONS, edges=EDGES, transfers=transfers_est,
                   headways=HEADWAYS)
    r = g.route({1: 0}, {5: 0})
    assert r.transfers[0].estimated is True
    assert r.estimated is True


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


# --- Task 11: nearest_stations / door_to_door — движок «дверь-дверь» -------

def test_nearest_stations_returns_plain_seconds_dict(conn):
    # Публичный контракт задачи: dict[int, int], без признака оценки — на
    # нём завязан SQL-фильтр Задачи 12.
    upsert_transit([_line()], conn, "msk", _curated())
    out = nearest_stations(conn, "msk", 37.60, 55.75, walker=None)
    assert out and all(isinstance(v, int) for v in out.values())


def test_door_to_door_reports_absence_for_unknown_city(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    assert door_to_door(conn, "spb", (37.60, 55.75), (37.64, 55.75)) is None


def test_door_to_door_reports_absence_when_target_unreachable(conn):
    # Два несвязанных острова: разные имена станций не образуют пересадку,
    # граф остаётся разбит на две компоненты — дом у одной, цель у другой.
    upsert_transit([
        _line("1", names=("A", "B", "C"), lon0=37.60),
        _line("9", names=("X", "Y", "Z"), lon0=38.60),
    ], conn, "msk", _curated())
    home = (37.60, 55.75)     # рядом с "A"
    dest = (38.60, 55.75)     # рядом с "X" — другая компонента графа
    assert door_to_door(conn, "msk", home, dest, walker=None) is None


def test_door_to_door_total_minutes_do_not_double_count_the_destination_walk(conn):
    # route.ride_seconds УЖЕ включает оба пеших плеча (докстрока route(),
    # R56: `ride_seconds == times_from(seeds)[t] + walk`) — это полное время
    # от двери до двери. Самопроверка через прямой вызов route() с теми же
    # seeds/targets ловит повторное прибавление пешего плеча цели.
    #
    # dest НАМЕРЕННО не совпадает с координатами станции "C" (это дало бы
    # targets[C] == 0 и замаскировало бы двойной счёт нулём) — смещение
    # ~130 м даёт targets[C] заметно больше нуля.
    upsert_transit([_line()], conn, "msk", _curated())
    home, dest = (37.60, 55.75), (37.641, 55.751)   # рядом с "A" и с "C"
    got = door_to_door(conn, "msk", home, dest, walker=None)
    assert got is not None
    ride, _geometry = got

    graph = load_graph(conn, "msk")
    seeds = nearest_stations(conn, "msk", *home, walker=None)
    targets = nearest_stations(conn, "msk", *dest, walker=None)
    expected = graph.route(seeds, targets)
    assert ride.total_minutes == max(1, round(expected.ride_seconds / 60))


def test_door_to_door_segment_colour_field_maps_to_schema_colour(conn):
    # Segment.colour (внутренний граф) обязан лечь в MetroSegment.colour.
    upsert_transit([_line()], conn, "msk", _curated())
    ride, _ = door_to_door(conn, "msk", (37.60, 55.75), (37.64, 55.75), walker=None)
    assert ride.segments and ride.segments[0].colour == "#EF161E"


def test_door_to_door_ors_in_walking_leg_estimate_r54(conn):
    # Controller ruling R54/R57: рельсовая часть маршрута полностью
    # курирована (все headway/edges — из _curated()), но пешее плечо
    # (walker=None → straight_walk_seconds-фолбэк) обязано покрасить
    # MetroRide.estimated в True — иначе оценочное пешее плечо покажется
    # пользователю как измеренный факт.
    upsert_transit([_line()], conn, "msk", _curated())
    ride, _ = door_to_door(conn, "msk", (37.60, 55.75), (37.64, 55.75), walker=None)
    assert not ride.segments[0].estimated, "рельсовая часть курирована"
    assert ride.estimated is True, "оценочное пешее плечо обязано покрасить итог"


def _rough_metres(a, b):
    # Пропорционален реальному расстоянию (в отличие от плоской константы) —
    # плоский walker искусственно уравнивает все k кандидатов и делает
    # "пеший заход до дальней станции" обманчиво дешёвым, отчего trivial-путь
    # (R66) начинает выигрывать у настоящей рельсовой поездки.
    dx = (b[0] - a[0]) * 111_320 * math.cos(math.radians(a[1]))
    dy = (b[1] - a[1]) * 111_320
    return (dx ** 2 + dy ** 2) ** 0.5


def test_door_to_door_walking_leg_not_estimated_when_walker_succeeds(conn):
    # Тот же курированный граф, но живой walker отвечает на все вызовы —
    # оба пеших плеча измерены, итог не должен красить True из-за них.
    upsert_transit([_line()], conn, "msk", _curated())
    ride, _ = door_to_door(conn, "msk", (37.60, 55.75), (37.64, 55.75),
                           walker=lambda a, b: _rough_metres(a, b) / 1.3)
    assert ride is not None
    assert ride.estimated is False


def test_door_to_door_walker_failure_on_one_station_still_taints_estimate(conn):
    # per-station отказ (как в Задаче 7, test_walker_failure_degrades_
    # per_station_not_globally): часть кандидатов провалилась — оценка
    # обязана всплыть, даже если другие кандидаты той же точки ответили
    # успешно.
    upsert_transit([_line()], conn, "msk", _curated())

    def flaky(start, end):
        if end[0] > 37.62:   # дальние кандидаты недоступны сети
            raise RuntimeError("сеть недоступна")
        return _rough_metres(start, end) / 1.3

    ride, _ = door_to_door(conn, "msk", (37.60, 55.75), (37.64, 55.75), walker=flaky)
    assert ride is not None
    assert ride.estimated is True


# --- Фикс-раунд 1 -----------------------------------------------------------

# --- R63: потолок пешего плеча — target вне зоны охвата графа отдаёт absence

def test_nearest_stations_detailed_drops_candidates_beyond_the_cap(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    far = _nearest_stations_detailed(conn, "msk", 38.60, 56.75, walker=None)
    assert far == {}, "все три станции линии дальше MAX_ENTRY_WALK_METRES"


def test_door_to_door_reports_absence_when_target_walk_exceeds_the_cap(conn):
    # R63 (фикс-раунд 1): цель геокодирована далеко за пределы реальной зоны
    # охвата графа (соседний город, ошибка геокодера) — ни одна платформа не
    # должна попасть в ответ вместо честной, но абсурдной оценки в часах.
    upsert_transit([_line()], conn, "msk", _curated())
    home = (37.60, 55.75)          # рядом с "A"
    dest = (38.60, 56.75)          # десятки км от единственной линии графа
    assert door_to_door(conn, "msk", home, dest, walker=None) is None


def test_max_entry_walk_metres_is_generous_enough_for_a_real_in_city_walk():
    # Потолок не должен отсекать легитимный дальний, но реальный пеший подход
    # (пример из докстроки константы — не даёт регрессировать в 100-метровый
    # потолок, который отсекал бы обычные дворы).
    assert MAX_ENTRY_WALK_METRES >= 2000


# --- R64: MetroRide.wait_min и реальный инвариант суммы частей -------------

def test_door_to_door_wait_min_reconciles_parts_to_total_on_a_single_line_ride(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    home, dest = (37.60, 55.75), (37.641, 55.751)   # рядом с "A" и с "C"
    ride, _ = door_to_door(conn, "msk", home, dest, walker=None)
    assert ride is not None
    parts = (ride.walk_from_home_min + ride.walk_to_dest_min
             + sum(s.minutes for s in ride.segments)
             + sum(t.minutes for t in ride.transfers) + ride.wait_min)
    assert parts == ride.total_minutes
    assert ride.wait_min > 0, "headway линии 1 (120 с) обязан быть виден"


def _curated_two_lines():
    c = CuratedTimes()
    c.headways = {"1": 120, "2": 300}
    c.speeds = {"1": 40.0, "2": 40.0}
    c.edges = {("1", "y", "b"): 150, ("1", "b", "c"): 150,
              ("2", "b", "x"): 200, ("2", "x", "y2"): 200}
    return c


def test_door_to_door_wait_min_reconciles_parts_to_total_on_a_ride_with_a_transfer(conn):
    upsert_transit([
        _line("1", names=("Y", "B", "C"), lon0=37.60),
        _line("2", names=("B", "X", "Y2"), lon0=37.62),
    ], conn, "msk", _curated_two_lines())
    home = (37.60, 55.75)      # рядом с "Y" на линии 1
    dest = (37.661, 55.751)    # рядом с "Y2" на линии 2, после пересадки в B
    ride, _ = door_to_door(conn, "msk", home, dest, walker=None)
    assert ride is not None
    assert ride.transfers, "сценарий обязан пройти через пересадку в B"
    parts = (ride.walk_from_home_min + ride.walk_to_dest_min
             + sum(s.minutes for s in ride.segments)
             + sum(t.minutes for t in ride.transfers) + ride.wait_min)
    assert parts == ride.total_minutes
    assert ride.wait_min > 0, "headway посадки + headway после пересадки обязаны быть видны"


def test_door_to_door_wait_min_tracks_the_actual_route_wait_seconds(conn):
    # Инвариант выше держится по построению (wait_min — остаток), это само
    # по себе не доказывает, что остаток отражает РЕАЛЬНОЕ ожидание, а не
    # произвольное число. Сверяем с route.wait_seconds того же запроса.
    upsert_transit([_line()], conn, "msk", _curated())
    home, dest = (37.60, 55.75), (37.641, 55.751)
    ride, _ = door_to_door(conn, "msk", home, dest, walker=None)
    graph = load_graph(conn, "msk")
    seeds = nearest_stations(conn, "msk", *home, walker=None)
    targets = nearest_stations(conn, "msk", *dest, walker=None)
    expected = graph.route(seeds, targets)
    assert abs(ride.wait_min - round(expected.wait_seconds / 60)) <= 1


# --- R65: пешее плечо и геометрия обязаны отражать станцию, которую выбрал
#          route(), а не ближайшую по прямой из seeds/targets --------------

def _curated_with_isolated_platform():
    c = CuratedTimes()
    c.headways = {"1": 120, "9": 120}
    c.speeds = {"1": 40.0, "9": 40.0}
    c.edges = {("1", "y", "b"): 150, ("1", "b", "c"): 150}
    return c


def test_door_to_door_walk_minutes_reflect_the_station_route_actually_chose(conn):
    # "X" стоит буквально на пороге дома (walk≈0), но изолирована — своя
    # линия без единого ребра, доехать через неё никуда нельзя. Настоящий
    # вход — "Y", 100+ м пешком. До фикса home_walk брался как
    # min(seeds.values()) — 0 мин через X, хотя реальный маршрут вошёл через Y.
    upsert_transit([
        _line("1", names=("Y", "B", "C"), lon0=37.60),
        _line("9", names=("X",), lon0=37.5991),
    ], conn, "msk", _curated_with_isolated_platform())
    home = (37.599, 55.75)
    dest = (37.641, 55.751)   # рядом с "C"
    ride, geometry = door_to_door(conn, "msk", home, dest, walker=None)
    assert ride is not None
    assert ride.walk_from_home_min > 0, "вход через тупиковую X не должен победить"
    # первая точка геометрии после дома — не координаты X (0 м от дома)
    assert geometry[1] != geometry[0]


# --- R66: защита от устаревшего id, честный 0 для пеших плеч, dedup,
#          absence вместо MetroRide с пустыми segments -----------------------

def test_door_to_door_filters_stale_station_ids_before_routing(conn, monkeypatch):
    # id платформы мог устареть между запросом nearest_stations и текущим
    # отпечатком графа (например, параллельная пересборка). Подмешиваем
    # несуществующий id с самым дешёвым "пешим" временем — до фикса это было
    # неограждённое graph.stations[...] и падение с KeyError.
    upsert_transit([_line()], conn, "msk", _curated())
    import habitus.online.metro_route as mod

    real = mod._nearest_stations_detailed

    def poisoned(conn_, city, lon, lat, k=3, walker=None):
        out = dict(real(conn_, city, lon, lat, k, walker=walker))
        out[999999] = (1, True)   # id вне графа, самый дешёвый по времени
        return out

    monkeypatch.setattr(mod, "_nearest_stations_detailed", poisoned)
    ride, _ = mod.door_to_door(conn, "msk", (37.60, 55.75), (37.641, 55.751),
                               walker=None)
    assert ride is not None


def test_door_to_door_allows_zero_walk_and_dedupes_geometry_when_home_is_on_the_platform(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    home = (37.60, 55.75)     # ровно на "A"
    dest = (37.641, 55.751)   # рядом с "C" — реальная рельсовая часть есть
    ride, geometry = door_to_door(conn, "msk", home, dest, walker=None)
    assert ride is not None
    assert ride.walk_from_home_min == 0
    assert all(geometry[i] != geometry[i + 1] for i in range(len(geometry) - 1))


def test_door_to_door_returns_absence_for_a_trivial_route(conn):
    # Дом и цель ближе всего к одной и той же платформе — это не
    # метро-поездка (два пеших плеча без рельсовой части). MetroRide с
    # пустыми segments не должен доехать до пользователя.
    upsert_transit([_line()], conn, "msk", _curated())
    home = (37.6199, 55.7501)   # у "B" с одной стороны
    dest = (37.6201, 55.7499)   # у "B" с другой стороны
    assert door_to_door(conn, "msk", home, dest, walker=None) is None
