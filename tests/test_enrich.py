import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.enrich import enrich_all, enrich_around


def _seed(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, poi;")
        # квартира в центре
        cur.execute("""INSERT INTO listings (external_id, source, geom)
            VALUES ('L1','kaggle', ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326));""")
        # два бара в ~200м и школа в ~300м
        cur.execute("""INSERT INTO poi (osm_id, kind, geom) VALUES
            (1,'bar', ST_SetSRID(ST_MakePoint(37.6195,55.7560),4326)),
            (2,'bar', ST_SetSRID(ST_MakePoint(37.6150,55.7550),4326)),
            (3,'school', ST_SetSRID(ST_MakePoint(37.6210,55.7560),4326));""")
    conn.commit()


def test_enrich_all_computes_density_and_walk():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed(conn)
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT bar_density_500m, walk_min_school
                           FROM listings WHERE external_id='L1';""")
            density, walk_school = cur.fetchone()
        assert density == 2          # оба бара в 500м
        assert 0 < walk_school < 15  # школа близко, разумные минуты


def test_enrich_around_only_touches_nearby():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            # near — рядом с точкой пересчёта; far — ~5км в стороне
            cur.execute("""INSERT INTO listings (external_id, source, geom) VALUES
                ('near','kaggle', ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326)),
                ('far','kaggle',  ST_SetSRID(ST_MakePoint(37.7000,55.7558),4326));""")
            cur.execute("""INSERT INTO poi (osm_id, kind, geom) VALUES
                (10,'bar', ST_SetSRID(ST_MakePoint(37.6180,55.7560),4326));""")
        conn.commit()
        affected = enrich_around(conn, "POINT(37.6173 55.7558)")
        with conn.cursor() as cur:
            cur.execute("SELECT bar_density_500m FROM listings WHERE external_id='near';")
            near_density = cur.fetchone()[0]
            cur.execute("SELECT bar_density_500m FROM listings WHERE external_id='far';")
            far_density = cur.fetchone()[0]
        assert affected == 1          # затронут только near
        assert near_density == 1
        assert far_density is None    # far не пересчитывался


def test_enrich_around_wkt_not_injectable():
    """Вредоносный WKT должен трактоваться как данные, а не исполняться."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO listings (external_id, source, geom)
                VALUES ('L1','kaggle', ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326));""")
        conn.commit()
        evil = "POINT(37.6 55.7)'); DROP TABLE listings;--"
        # невалидная геометрия → ошибка парсинга, а НЕ выполнение DROP
        with pytest.raises(psycopg.Error):
            enrich_around(conn, evil)
        conn.rollback()
        # таблица listings цела
        with conn.cursor() as cur:
            cur.execute("SELECT to_regclass('listings');")
            assert cur.fetchone()[0] == "listings"
            cur.execute("SELECT count(*) FROM listings;")
            assert cur.fetchone()[0] == 1
