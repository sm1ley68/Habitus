import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.update.incremental import apply_new_poi, deactivate_missing
from habitus.ingest.kaggle_loader import load_to_raw
from habitus.clean.normalize import promote_to_listings


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


def _seed_two_sources(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings;")
        for eid, src in (("cian_1", "cian"), ("cian_2", "cian"), ("kag_1", "kaggle")):
            cur.execute("""INSERT INTO listings (external_id, source, is_active, geom)
                VALUES (%s,%s,TRUE,ST_SetSRID(ST_MakePoint(37.6,55.75),4326));""",
                        (eid, src))
    conn.commit()


def test_deactivate_missing_is_scoped_to_its_source():
    """Снимок Циана не должен гасить объявления другого источника: списки
    external_id у них не пересекаются, и без скоупа один прогон убил бы всё чужое."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_two_sources(conn)
        gone = deactivate_missing({"cian_1"}, conn, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT external_id FROM listings WHERE is_active ORDER BY 1;")
            alive = [r[0] for r in cur.fetchall()]
    assert gone == 1                      # погашен только cian_2
    assert alive == ["cian_1", "kag_1"]   # чужой источник цел


def test_reappearing_listing_comes_back_active():
    """Объявление, вернувшееся в выдачу источника, должно снова стать активным —
    иначе повторно выставленная квартира навсегда пропадёт из поиска."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_two_sources(conn)
        deactivate_missing({"cian_1"}, conn, source="cian")
        row = {"external_id": "cian_2", "source": "cian", "price": 20_000_000,
               "area": 54.0, "kitchen_area": None, "rooms": 2, "level": 3,
               "levels": 9, "building_type": None, "object_type": None,
               "lat": 55.75, "lon": 37.62, "description": "снова в продаже",
               "city": "msk", "address": None, "source_url": None,
               "source_extra": {}, "photos": []}
        load_to_raw([row], conn)
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='cian_2';")
            assert cur.fetchone()[0] is True
