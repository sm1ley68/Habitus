# tests/test_normalize.py
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
from habitus.clean.normalize import is_valid, promote_to_listings
from pathlib import Path

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"

def test_is_valid_rejects_garbage():
    assert is_valid({"price": 12000000, "area": 54.0, "lat": 55.75, "lon": 37.61})
    assert not is_valid({"price": 0, "area": 54.0, "lat": 55.75, "lon": 37.61})
    assert not is_valid({"price": 12000000, "area": 54.0, "lat": 0.0, "lon": 0.0})
    assert not is_valid({"price": 12000000, "area": 2.0, "lat": 55.75, "lon": 37.61})

def test_promote_sets_geom_and_is_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE raw_listings, listings;")
        conn.commit()
        load_to_raw(parse_csv(FIX), conn)
        n1 = promote_to_listings(conn)
        n2 = promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT count(*), count(geom) FROM listings;")
            total, with_geom = cur.fetchone()
        assert n1 == 2
        assert total == 2 and with_geom == 2
