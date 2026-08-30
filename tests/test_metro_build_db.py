import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro import LineRaw, StationRaw, normalize_station_name, upsert_transit
from habitus.geo.metro_times import CuratedTimes


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE metro_line CASCADE;")
        c.commit()
        yield c


def _line(ref="1", system="subway", names=("A", "B", "C"), lon0=37.60,
          ring=False):
    return LineRaw(
        system=system, ref=ref, name=f"линия {ref}", colour="#EF161E",
        stations=[StationRaw(osm_id=1000 + i, name=n, lon=lon0 + i * 0.02,
                             lat=55.75)
                  for i, n in enumerate(names)],
        geometry=[[lon0 + i * 0.02, 55.75] for i in range(len(names))],
        ring=ring)


def _curated():
    c = CuratedTimes()
    c.headways = {"1": 120, "2": 120}
    c.speeds = {"1": 40.0, "2": 40.0}
    c.edges = {("1", "a", "b"): 150, ("1", "b", "a"): 150}
    return c


def test_stations_keep_order_and_normalized_name(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    rows = conn.execute(
        "SELECT name, name_norm, order_index FROM metro_station ORDER BY order_index"
    ).fetchall()
    assert [r[0] for r in rows] == ["A", "B", "C"]
    assert [r[1] for r in rows] == ["a", "b", "c"]
    assert [r[2] for r in rows] == [0, 1, 2]


def test_edges_link_consecutive_stations_both_ways(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    n = conn.execute("SELECT count(*) FROM metro_edge").fetchone()[0]
    assert n == 4   # A↔B и B↔C, по два направления


def test_curated_edge_wins_and_missing_one_is_marked(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    rows = dict(conn.execute("""
        SELECT s1.name || '-' || s2.name, e.estimated
        FROM metro_edge e
        JOIN metro_station s1 ON s1.id = e.from_station
        JOIN metro_station s2 ON s2.id = e.to_station""").fetchall())
    assert rows["A-B"] is False        # есть в курируемом файле
    assert rows["B-C"] is True         # нет — оценка, помечена


def test_transfer_created_between_same_named_stations_on_other_lines(conn):
    upsert_transit([_line("1", names=("A", "B", "C")),
                    _line("2", names=("B", "X", "Y"), lon0=37.62)],
                   conn, "msk", _curated())
    rows = conn.execute("""
        SELECT s1.name, s2.name FROM metro_transfer t
        JOIN metro_station s1 ON s1.id = t.from_station
        JOIN metro_station s2 ON s2.id = t.to_station""").fetchall()
    # одноимённая станция на двух линиях — это пересадочный узел
    assert {(a, b) for a, b in rows} == {("B", "B")}


def test_line_geometry_stored(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    row = conn.execute(
        "SELECT ST_GeometryType(geom) FROM metro_line_geom").fetchone()
    assert row[0] == "ST_LineString"


def test_rerun_does_not_duplicate(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    before = conn.execute("SELECT count(*) FROM metro_station").fetchone()[0]
    upsert_transit([_line()], conn, "msk", _curated())
    after = conn.execute("SELECT count(*) FROM metro_station").fetchone()[0]
    assert before == after == 3


def test_headway_comes_from_curated_data(conn):
    upsert_transit([_line()], conn, "msk", _curated())
    assert conn.execute(
        "SELECT headway_s FROM metro_line WHERE ref='1'").fetchone()[0] == 120


def test_headway_estimated_flag_reflects_curated_hit(conn):
    # ref='1' курирован (_curated().headways содержит '1') — headway_estimated
    # обязан быть FALSE. Значение "выглядит как измеренное" не должно молча
    # оказаться оценкой (R29/R30).
    upsert_transit([_line()], conn, "msk", _curated())
    assert conn.execute(
        "SELECT headway_estimated FROM metro_line WHERE ref='1'"
    ).fetchone()[0] is False


def test_headway_estimated_flag_set_when_line_not_curated(conn):
    # ref='9' нет ни в headways, ни где-либо ещё в _curated() — это
    # некурированная линия, headway_seconds() обязана вернуть пессимистичный
    # дефолт и пометить его estimated=True (R29/R30 — колонка headway_estimated
    # существует ровно для этого случая).
    upsert_transit([_line(ref="9")], conn, "msk", _curated())
    row = conn.execute(
        "SELECT headway_s, headway_estimated FROM metro_line WHERE ref='9'"
    ).fetchone()
    assert row[1] is True
    assert row[0] > 0  # пессимистичный дефолт по системе, не ноль


def test_ring_line_gets_closing_edge(conn):
    # R24: ring должен замыкаться явным ребром последняя→первая станция —
    # иначе стык кольца непроходим напрямую.
    upsert_transit([_line("14", "mck", names=("A", "B", "C"), ring=True)],
                   conn, "msk", _curated())
    n = conn.execute("SELECT count(*) FROM metro_edge").fetchone()[0]
    assert n == 6  # A-B, B-C, C-A — каждое ребро в обе стороны
    rows = {(a, b) for a, b in conn.execute("""
        SELECT s1.name, s2.name FROM metro_edge e
        JOIN metro_station s1 ON s1.id = e.from_station
        JOIN metro_station s2 ON s2.id = e.to_station""").fetchall()}
    assert ("C", "A") in rows and ("A", "C") in rows


def test_non_ring_line_has_no_closing_edge(conn):
    # R24 (вторая половина): на обычной линии закрывающего ребра быть не
    # должно — это была бы фабрикация несуществующего перегона.
    upsert_transit([_line("1", "subway", names=("A", "B", "C"), ring=False)],
                   conn, "msk", _curated())
    n = conn.execute("SELECT count(*) FROM metro_edge").fetchone()[0]
    assert n == 4  # A-B, B-C — без C-A
    rows = {(a, b) for a, b in conn.execute("""
        SELECT s1.name, s2.name FROM metro_edge e
        JOIN metro_station s1 ON s1.id = e.from_station
        JOIN metro_station s2 ON s2.id = e.to_station""").fetchall()}
    assert ("C", "A") not in rows and ("A", "C") not in rows


def test_transfer_outdoor_flag_traces_to_curated_data(conn):
    # R32: outdoor=FALSE не должен означать "не проверяли" — для некурированной
    # пары (нет ни в transfers, ни в outdoor) значение обязано прийти из
    # transfer_seconds() как есть, а не быть переизобретено здесь. Признак
    # оценки идёт рядом: estimated=True сигнализирует, что outdoor тоже не
    # измерен, а не что переход подтверждённо подземный.
    upsert_transit([_line("1", names=("A", "B", "C")),
                    _line("2", names=("B", "X", "Y"), lon0=37.62)],
                   conn, "msk", _curated())
    row = conn.execute("""
        SELECT t.estimated, t.outdoor FROM metro_transfer t
        JOIN metro_station s1 ON s1.id = t.from_station
        JOIN metro_station s2 ON s2.id = t.to_station
        WHERE s1.name = 'B' AND s2.name = 'B'""").fetchone()
    assert row[0] is True    # не в _curated().transfers — оценка
    assert row[1] is False   # DEFAULT_TRANSFER_S — не помечен outdoor


def _curated_with_named_transfer(outdoor=True):
    # Реальный случай из data/metro/msk.json: пересадка между станциями с
    # РАЗНЫМИ именами («Охотный Ряд» ↔ «Театральная») — self-join по
    # name_norm её не находит, это ровно то, что чинит фикс-раунд 1 (R33).
    c = _curated()
    key = tuple(sorted([normalize_station_name("P"), normalize_station_name("Q")]))
    c.transfers[key] = 240
    if outdoor:
        c.outdoor.add(key)
    return c


def test_curated_transfer_between_differently_named_stations_is_created(conn):
    # R33: без ветки, резолвящей курируемые пары через name_norm, эта
    # пересадка не появлялась в БД вообще (та же форма бага, что и в
    # data/metro/msk.json — «Охотный Ряд» и «Театральная» не совпадают по
    # имени, self-join их не связывает).
    upsert_transit([_line("1", names=("P", "M1")),
                    _line("2", names=("Q", "M2"), lon0=37.62)],
                   conn, "msk", _curated_with_named_transfer(outdoor=True))
    row = conn.execute("""
        SELECT t.seconds, t.estimated, t.outdoor FROM metro_transfer t
        JOIN metro_station s1 ON s1.id = t.from_station
        JOIN metro_station s2 ON s2.id = t.to_station
        WHERE s1.name = 'P' AND s2.name = 'Q'""").fetchone()
    assert row is not None, "курируемая пересадка P↔Q не создана"
    seconds, estimated, outdoor = row
    assert seconds == 240
    assert estimated is False  # курировано — не оценка (R34)
    assert outdoor is True     # значение обязано прийти из курируемых данных (R32/R34)


def test_curated_transfer_resolving_to_no_station_creates_no_row(conn):
    # "Курируемое имя, которого нет среди станций города, просто не даёт ни
    # одной строки" — часть R33: резолвер не имеет права изобрести станцию
    # или пересадку под несуществующее имя.
    stats = upsert_transit([_line("1", names=("A", "B", "C"))],
                           conn, "msk", _curated_with_named_transfer())
    n = conn.execute("SELECT count(*) FROM metro_transfer").fetchone()[0]
    assert n == 0
    assert stats["transfers"] == 0


def test_degenerate_two_station_ring_does_not_double_count_edges(conn):
    # R35: у кольца из двух станций замыкающая пара (последняя, первая)
    # совпадает с уже учтённой consecutive-парой (0, 1) — это одна связь, а
    # не две. Без len(ids) >= 3 та же пара вставлялась бы дважды подряд, и
    # stats["edges"] лгал бы о количестве реально вставленных строк.
    stats = upsert_transit([_line("14", "mck", names=("A", "B"), ring=True)],
                           conn, "msk", _curated())
    n = conn.execute("SELECT count(*) FROM metro_edge").fetchone()[0]
    assert n == 2          # A-B и B-A, не 4
    assert stats["edges"] == 2
