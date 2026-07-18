from pathlib import Path

import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.zones import (backfill_listing_zones, import_admin_geojson,
                               import_named_seed)

FIX = Path(__file__).parent / "fixtures" / "zones_sample.geojson"
SEED = Path(__file__).resolve().parents[1] / "data" / "named_zones.seed.json"


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE admin_zones, named_zones, listings RESTART IDENTITY;")
        c.commit()
        yield c


def test_import_admin_and_backfill(conn):
    assert import_admin_geojson(FIX, conn) == 3
    with conn.cursor() as cur:
        cur.execute("""INSERT INTO listings (external_id, source, geom) VALUES
            ('IN_HAM','test', ST_SetSRID(ST_MakePoint(37.60,55.735),4326)),
            ('IN_SAO','test', ST_SetSRID(ST_MakePoint(37.60,55.86),4326));""")
    conn.commit()
    assert backfill_listing_zones(conn) == 2
    rows = dict(conn.execute("SELECT external_id, okrug FROM listings ORDER BY 1").fetchall())
    assert rows["IN_HAM"] == "ЦАО" and rows["IN_SAO"] == "САО"
    ham_raion = conn.execute("SELECT raion FROM listings WHERE external_id='IN_HAM'").fetchone()[0]
    assert ham_raion == "Хамовники"
    # parent района проставлен округом
    parent = conn.execute("SELECT parent FROM admin_zones WHERE kind='raion' AND name='Хамовники'").fetchone()[0]
    assert parent == "ЦАО"


def test_import_admin_is_idempotent(conn):
    import_admin_geojson(FIX, conn)
    import_admin_geojson(FIX, conn)
    n = conn.execute("SELECT count(*) FROM admin_zones").fetchone()[0]
    assert n == 3


def test_import_named_seed(conn):
    assert import_named_seed(SEED, conn) >= 5
    row = conn.execute("SELECT lon, lat FROM named_zones WHERE 'Патрики' = ANY(aliases)").fetchone()
    assert row is not None and 37.5 < row[0] < 37.7
