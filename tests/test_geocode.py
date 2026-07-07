import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.clean.geocode import backfill_missing_coords

def test_backfill_uses_injected_geocoder():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, description)
                           VALUES ('t1','cian','ул. Тверская, 1');""")
        conn.commit()
        fake = lambda addr, session=None: (37.6113, 55.7570)
        n = backfill_missing_coords(conn, geocoder=fake)
        with conn.cursor() as cur:
            cur.execute("SELECT ST_X(geom), ST_Y(geom) FROM listings WHERE external_id='t1';")
            x, y = cur.fetchone()
        assert n == 1
        assert abs(x - 37.6113) < 1e-4 and abs(y - 55.7570) < 1e-4
