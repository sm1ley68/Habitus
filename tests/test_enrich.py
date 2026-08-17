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


def test_source_metro_time_wins_over_computed():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            # станция в ~1.5 км → вычисленное время ~19 мин
            cur.execute("""INSERT INTO poi (osm_id, kind, name, geom, city) VALUES
                (1,'metro','Дальняя',ST_SetSRID(ST_MakePoint(37.63,55.75),4326),'msk');""")
            cur.execute("""INSERT INTO listings (external_id, source, geom, city,
                                                 walk_min_metro_src) VALUES
                ('WITH_SRC','t',ST_SetSRID(ST_MakePoint(37.61,55.75),4326),'msk',7),
                ('NO_SRC','t',ST_SetSRID(ST_MakePoint(37.61,55.75),4326),'msk',NULL);""")
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT external_id, walk_min_metro FROM listings ORDER BY external_id;")
            got = dict(cur.fetchall())
    assert got["WITH_SRC"] == 7.0            # источник не перезаписан
    assert got["NO_SRC"] > 10                # посчитано по OSM


def test_enrich_does_not_cross_cities():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO poi (osm_id, kind, name, geom, city) VALUES
                (2,'school','Чужая',ST_SetSRID(ST_MakePoint(37.611,55.75),4326),'dxb');""")
            cur.execute("""INSERT INTO listings (external_id, source, geom, city) VALUES
                ('M1','t',ST_SetSRID(ST_MakePoint(37.61,55.75),4326),'msk');""")
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT walk_min_school FROM listings WHERE external_id='M1';")
            assert cur.fetchone()[0] is None    # школа чужого города не считается


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


def test_noise_has_three_grades_from_evidence():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("DELETE FROM urban_evidence;")
            # модельные дБ рядом с тремя объектами
            for eid, lon, db in (("Q", 37.60, 45.0), ("M", 37.62, 60.0), ("L", 37.64, 70.0)):
                cur.execute("""INSERT INTO listings (external_id, source, geom, city)
                    VALUES (%s,'t',ST_SetSRID(ST_MakePoint(%s,55.75),4326),'msk');""",
                            (eid, lon))
                cur.execute("""INSERT INTO urban_evidence
                    (source_id, source, city, layer, geom, db, observed_at)
                    VALUES (%s,'test','msk','noise',
                            ST_SetSRID(ST_MakePoint(%s,55.75),4326),%s,now());""",
                            (eid, lon, db))
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT external_id, noise_level FROM listings ORDER BY external_id;")
            got = dict(cur.fetchall())
    assert got == {"L": "high", "M": "medium", "Q": "low"}


def test_noise_grades_are_relative_thirds_of_the_city():
    """Слой шума модельный: у него всего несколько дискретных значений и средние
    65 дБ, поэтому абсолютная шкала (55/65) отправляет весь город в medium/high и
    градация перестаёт нести информацию. Пороги берутся по перцентилям самой
    экспозиции — «тише двух третей города» есть всегда, даже если абсолютно
    тихих адресов в выборке нет."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("DELETE FROM urban_evidence;")
            for i in range(9):                      # 60..68 дБ — все «шумные»
                eid, lon, db = f"n{i}", 37.60 + i * 0.02, 60.0 + i
                cur.execute("""INSERT INTO listings (external_id, source, geom, city)
                    VALUES (%s,'t',ST_SetSRID(ST_MakePoint(%s,55.75),4326),'msk');""",
                            (eid, lon))
                cur.execute("""INSERT INTO urban_evidence
                    (source_id, source, city, layer, geom, db, observed_at)
                    VALUES (%s,'test','msk','noise',
                            ST_SetSRID(ST_MakePoint(%s,55.75),4326),%s,now());""",
                            (eid, lon, db))
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT noise_level, count(*) FROM listings GROUP BY 1;")
            got = dict(cur.fetchall())
    assert got == {"low": 3, "medium": 3, "high": 3}


# --- название ближайшей станции ------------------------------------------
#
# Циан отдаёт станции с transport_type; пеших среди них может не быть вовсе,
# и тогда metro_station оставался NULL — при том, что МИНУТЫ до метро уже
# считались по OSM-фолбэку (у 206 объявлений из 2143 именно так). Название
# станции лежало ровно в тех же точках poi, просто не записывалось.
def _seed_metro(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, poi;")
        cur.execute("""INSERT INTO listings (external_id, source, geom, metro_station)
            VALUES ('L_NO_NAME','cian',
                    ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326), NULL),
                   ('L_FROM_SOURCE','cian',
                    ST_SetSRID(ST_MakePoint(37.6175,55.7559),4326), 'Охотный Ряд');""")
        cur.execute("""INSERT INTO poi (osm_id, kind, name, geom) VALUES
            (10,'metro','Театральная', ST_SetSRID(ST_MakePoint(37.6190,55.7575),4326)),
            (11,'metro','Тверская',    ST_SetSRID(ST_MakePoint(37.6060,55.7640),4326));""")
    conn.commit()


def test_enrich_fills_station_name_from_nearest_poi():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_metro(conn)
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT metro_station, walk_min_metro
                           FROM listings WHERE external_id='L_NO_NAME';""")
            station, minutes = cur.fetchone()
        assert station == "Театральная"   # ближайшая, а не первая попавшаяся
        assert minutes is not None


def test_source_station_is_not_overwritten_by_computed_one():
    """Данные источника приоритетнее вычисленных: если Циан назвал станцию,
    вычисленная не имеет права её затереть."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_metro(conn)
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT metro_station FROM listings
                           WHERE external_id='L_FROM_SOURCE';""")
            assert cur.fetchone()[0] == "Охотный Ряд"


def test_no_metro_poi_leaves_station_empty():
    """Нет точек метро — станция остаётся NULL. Придумывать название нельзя."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO listings (external_id, source, geom)
                VALUES ('L_ALONE','cian',
                        ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326));""")
        conn.commit()
        enrich_all(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT metro_station FROM listings WHERE external_id='L_ALONE';")
            assert cur.fetchone()[0] is None
