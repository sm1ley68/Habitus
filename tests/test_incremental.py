import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.update.incremental import apply_new_poi, deactivate_missing


def test_new_bar_recomputes_nearby_density():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO listings (external_id, source, geom, bar_density_500m)
                VALUES ('L1','kaggle',
                    ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326), 0);""")
        conn.commit()
        new_bar = [{"osm_id": 999, "kind": "bar", "name": "Новый бар",
                    "lat": 55.7560, "lon": 37.6180}]
        affected = apply_new_poi(new_bar, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT bar_density_500m FROM listings WHERE external_id='L1';")
            density = cur.fetchone()[0]
        assert affected >= 1
        assert density == 1  # пересчиталось


def test_deactivate_missing():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, is_active)
                           VALUES ('A','cian',true),('B','cian',true);""")
        conn.commit()
        n = deactivate_missing({"A"}, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='B';")
            assert cur.fetchone()[0] is False
        assert n == 1
