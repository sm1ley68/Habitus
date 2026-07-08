import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.enrich import enrich_all


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
