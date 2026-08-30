import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro import LineRaw, StationRaw, upsert_transit
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
