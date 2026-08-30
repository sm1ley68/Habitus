import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.metro_access import (WALK_DETOUR, refresh_listing_metro_access,
                                      refresh_walk_min_metro,
                                      straight_walk_seconds)


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings CASCADE;")
            cur.execute("TRUNCATE metro_line CASCADE;")
            cur.execute("""INSERT INTO metro_line
                           (id, city, system, ref, name, headway_s, fallback_speed_kmh)
                           VALUES (1,'msk','subway','1','л1',120,40),
                                  (2,'msk','mcd','D1','д1',600,55);""")
            # три платформы: две подземных (ближе и дальше), одна МЦД —
            # ближе подземной «Дальней», но дальше подземной «Ближней».
            # «Ближняя» намеренно не в 30м, а в ~190м: на 30м её пешее время
            # по прямой (~31с) меньше порога МЦД-подмены ниже (60с) при любой
            # реализации фильтра — тест ничего бы не проверял. При ~190м
            # «Ближняя» (~184с) гарантированно больше искажённых 60с МЦД,
            # так что тест ловит именно подмешивание системы, а не саму
            # близость платформы.
            cur.execute("""INSERT INTO metro_station
                           (id, city, line_id, name, name_norm, geom, order_index)
                           VALUES
                           (10,'msk',1,'Ближняя','ближняя',
                            ST_SetSRID(ST_MakePoint(37.603,55.75),4326),0),
                           (11,'msk',1,'Дальняя','дальняя',
                            ST_SetSRID(ST_MakePoint(37.610,55.75),4326),1),
                           (12,'msk',2,'Диаметр','диаметр',
                            ST_SetSRID(ST_MakePoint(37.601,55.75),4326),0);""")
            cur.execute("""INSERT INTO listings (external_id, source, is_active,
                               city, geom)
                           VALUES ('A','test',TRUE,'msk',
                                   ST_SetSRID(ST_MakePoint(37.60,55.75),4326));""")
        c.commit()
        yield c


def test_straight_walk_applies_detour_factor():
    # прямая по воздуху занижает: между домом и станцией бывает река или пути
    assert straight_walk_seconds(1000) == int(round(1000 * WALK_DETOUR / 1.33))


def test_without_walker_falls_back_and_marks_estimated(conn):
    n = refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    assert n == 3
    rows = conn.execute(
        "SELECT station_id, estimated FROM listing_metro_access ORDER BY station_id"
    ).fetchall()
    assert [r[0] for r in rows] == [10, 11, 12]
    assert all(r[1] is True for r in rows)


def test_keeps_only_k_nearest(conn):
    refresh_listing_metro_access(conn, "msk", walker=None, k=2)
    ids = [r[0] for r in conn.execute(
        "SELECT station_id FROM listing_metro_access ORDER BY walk_seconds").fetchall()]
    assert len(ids) == 2 and 10 in ids


def test_network_walker_wins_over_straight_line(conn):
    # сеть возвращает 600 с там, где прямая дала бы меньше — значение из сети
    refresh_listing_metro_access(conn, "msk", walker=lambda a, b: 600.0, k=1)
    row = conn.execute(
        "SELECT walk_seconds, estimated FROM listing_metro_access").fetchone()
    assert row == (600, False)


def test_walker_failure_degrades_per_station_not_globally(conn):
    def flaky(a, b):
        raise RuntimeError("ORS упал")

    n = refresh_listing_metro_access(conn, "msk", walker=flaky, k=2)
    assert n == 2   # строки всё равно есть
    assert all(r[0] is True for r in
               conn.execute("SELECT estimated FROM listing_metro_access").fetchall())


def test_walk_min_metro_counts_subway_only(conn):
    # МЦД-платформа ближе подземной «Дальней», но в walk_min_metro попадать
    # не должна: поле и фильтр geo kind=metro остаются про подземку
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    conn.execute("UPDATE listing_metro_access SET walk_seconds = 60 WHERE station_id = 12;")
    conn.commit()
    refresh_walk_min_metro(conn, "msk")
    got = conn.execute(
        "SELECT walk_min_metro FROM listings WHERE external_id='A'").fetchone()[0]
    assert got > 1.5, "минуты взяты с платформы МЦД — так быть не должно"


def test_rerun_is_idempotent(conn):
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    n = conn.execute("SELECT count(*) FROM listing_metro_access").fetchone()[0]
    assert n == 3


# --- провенанс walk_min_metro / metro_station ------------------------------
#
# Раньше (habitus/geo/enrich.py, до Задачи 7) эти же гарантии проверялись
# через poi(kind='metro') и прямую по воздуху. Владелец полей переехал сюда;
# COALESCE(l.walk_min_metro_src, ...) в refresh_walk_min_metro — тот же
# принцип провенанса: данные источника (Циан) главнее вычисленных.


def test_walk_min_metro_fills_name_and_minutes_from_nearest_subway(conn):
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    refresh_walk_min_metro(conn, "msk")
    name, minutes = conn.execute(
        "SELECT metro_station, walk_min_metro FROM listings WHERE external_id='A'"
    ).fetchone()
    assert name == "Ближняя"     # ближайшая ПОДЗЕМНАЯ, не МЦД-«Диаметр»
    assert minutes is not None and minutes > 0


def test_walk_min_metro_prefers_source_over_computed(conn):
    conn.execute("""UPDATE listings SET walk_min_metro_src = 7,
                        metro_station = 'Из источника' WHERE external_id='A';""")
    conn.commit()
    refresh_listing_metro_access(conn, "msk", walker=None, k=3)
    refresh_walk_min_metro(conn, "msk")
    minutes, name = conn.execute(
        "SELECT walk_min_metro, metro_station FROM listings WHERE external_id='A'"
    ).fetchone()
    assert minutes == 7.0            # источник не перезаписан вычисленным
    assert name == "Из источника"    # то же для названия станции


def test_no_access_rows_leaves_walk_min_metro_null_not_zero(conn):
    """Синтетический ноль вместо отсутствующего замера запрещён: город без
    станций даёт 0 строк listing_metro_access, а не walk_min_metro = 0."""
    conn.execute("""INSERT INTO listings (external_id, source, is_active, city, geom)
                    VALUES ('B','test',TRUE,'spb',
                            ST_SetSRID(ST_MakePoint(30.3,59.9),4326));""")
    conn.commit()
    n = refresh_listing_metro_access(conn, "spb", walker=None, k=3)
    assert n == 0
    refresh_walk_min_metro(conn, "spb")
    got = conn.execute(
        "SELECT walk_min_metro FROM listings WHERE external_id='B'").fetchone()[0]
    assert got is None
