import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.metro_route import clear_graph_cache, metro_predicate


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        clear_graph_cache()
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings CASCADE;")
            cur.execute("TRUNCATE metro_line CASCADE;")
            cur.execute("""INSERT INTO metro_line
                (id, city, system, ref, name, headway_s, fallback_speed_kmh)
                VALUES (1,'msk','subway','1','л1',120,40);""")
            # A —600с— B —600с— C, цель у C
            cur.execute("""INSERT INTO metro_station
                (id, city, line_id, name, name_norm, geom, order_index) VALUES
                (10,'msk',1,'A','a',ST_SetSRID(ST_MakePoint(37.50,55.75),4326),0),
                (11,'msk',1,'B','b',ST_SetSRID(ST_MakePoint(37.60,55.75),4326),1),
                (12,'msk',1,'C','c',ST_SetSRID(ST_MakePoint(37.70,55.75),4326),2);""")
            cur.execute("""INSERT INTO metro_edge
                (city, from_station, to_station, seconds) VALUES
                ('msk',10,11,600),('msk',11,10,600),
                ('msk',11,12,600),('msk',12,11,600);""")
            # BLIZKO живёт у B (10 мин езды до C), DALEKO — у A (20 мин)
            for eid, lon in (("BLIZKO", 37.60), ("DALEKO", 37.50)):
                cur.execute("""INSERT INTO listings
                    (external_id, source, is_active, city, geom)
                    VALUES (%s,'test',TRUE,'msk',
                            ST_SetSRID(ST_MakePoint(%s,55.75),4326));""", (eid, lon))
            cur.execute("""INSERT INTO listing_metro_access
                (external_id, station_id, walk_seconds) VALUES
                ('BLIZKO',11,120),('DALEKO',10,120);""")
        c.commit()
        yield c


def _ids(conn, sql, params) -> set[str]:
    rows = conn.execute(
        f"SELECT external_id FROM listings WHERE {sql}", params).fetchall()
    return {r[0] for r in rows}


def test_predicate_keeps_only_listings_within_the_budget(conn):
    # цель — у станции C; 15 минут хватает от B, но не от A
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)
    assert _ids(conn, sql, params) == {"BLIZKO"}


def test_wider_budget_admits_the_far_one(conn):
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=40)
    assert _ids(conn, sql, params) == {"BLIZKO", "DALEKO"}


def test_walk_leg_counts_towards_the_budget(conn):
    # плечо DALEKO раздуто до 20 минут — в 40-минутный бюджет он больше не лезет
    conn.execute("UPDATE listing_metro_access SET walk_seconds = 1200 "
                 "WHERE external_id = 'DALEKO';")
    conn.commit()
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=40)
    assert _ids(conn, sql, params) == {"BLIZKO"}


def test_no_graph_for_city_returns_none(conn):
    assert metro_predicate(conn, "spb", 30.3, 59.93, minutes=40) is None


def test_predicate_is_fully_parameterized(conn):
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)
    # никакой склейки значений в текст запроса — только плейсхолдеры
    assert "%s" in sql and str(15 * 60) not in sql
