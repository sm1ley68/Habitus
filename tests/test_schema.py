import psycopg
from habitus.db.init_db import init_db
from habitus.config import settings

def test_init_db_creates_tables():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT to_regclass('listings'), to_regclass('poi');")
            listings, poi = cur.fetchone()
        assert listings == "listings"
        assert poi == "poi"

def test_extensions_present():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("SELECT extname FROM pg_extension;")
            names = {r[0] for r in cur.fetchall()}
        assert "postgis" in names
        assert "vector" in names
