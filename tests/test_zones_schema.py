import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db


def _cols(conn, table):
    rows = conn.execute(
        "SELECT column_name FROM information_schema.columns WHERE table_name=%s",
        (table,)).fetchall()
    return {r[0] for r in rows}


def test_zone_tables_and_columns_exist():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        assert {"kind", "name", "parent", "aliases", "geom"} <= _cols(conn, "admin_zones")
        assert {"name", "aliases", "lon", "lat", "radius_m"} <= _cols(conn, "named_zones")
        assert {"okrug", "raion"} <= _cols(conn, "listings")
