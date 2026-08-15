# tests/test_normalize.py
import json
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
from habitus.clean.normalize import is_valid, promote_to_listings, pick_walk_metro
from pathlib import Path

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"

def test_is_valid_rejects_garbage():
    assert is_valid({"price": 12000000, "area": 54.0, "lat": 55.75, "lon": 37.61})
    assert not is_valid({"price": 0, "area": 54.0, "lat": 55.75, "lon": 37.61})
    assert not is_valid({"price": 12000000, "area": 54.0, "lat": 0.0, "lon": 0.0})
    assert not is_valid({"price": 12000000, "area": 2.0, "lat": 55.75, "lon": 37.61})

def test_promote_reactivates_reappeared_listing():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE raw_listings, listings;")
        conn.commit()
        load_to_raw(parse_csv(FIX), conn)
        promote_to_listings(conn)
        # объявление ушло из выдачи → деактивировано
        with conn.cursor() as cur:
            cur.execute("UPDATE listings SET is_active=false;")
        conn.commit()
        # повторный прогон источника → снова появилось → реактивируется
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT bool_and(is_active) FROM listings;")
            assert cur.fetchone()[0] is True


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


def test_pick_walk_metro_takes_nearest_walk_entry():
    entries = [
        {"name": "Шаболовская", "minutes": 3, "mode": "transport"},
        {"name": "Ленинский проспект", "minutes": 7, "mode": "walk"},
        {"name": "Площадь Гагарина", "minutes": 10, "mode": "walk"},
    ]
    # 3 минуты — это автобусом, колонка называется walk_min: берём 7
    assert pick_walk_metro(entries) == ("Ленинский проспект", 7.0)


def test_pick_walk_metro_without_walk_entries():
    assert pick_walk_metro([{"name": "X", "minutes": 4, "mode": "transport"}]) == (None, None)
    assert pick_walk_metro([]) == (None, None)


def test_promote_carries_source_fields_into_listings():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE raw_listings, listings;")
            cur.execute("""
                INSERT INTO raw_listings (external_id, source, price, area, rooms,
                                          lat, lon, city, address, source_url, source_extra)
                VALUES ('cian_1','cian',20000000,55,2,55.71,37.59,'msk',
                        'Москва, 2-й Донской проезд','https://cian.ru/1',%s);""",
                        (json.dumps({"metro": [
                            {"name": "Ленинский проспект", "minutes": 7, "mode": "walk"}],
                            "zhk": "SHIFT"}),))
        conn.commit()
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT city, address, source_url, metro_station,
                                  walk_min_metro_src, source_extra->>'zhk'
                           FROM listings WHERE external_id='cian_1';""")
            row = cur.fetchone()
    assert row == ("msk", "Москва, 2-й Донской проезд", "https://cian.ru/1",
                   "Ленинский проспект", 7.0, "SHIFT")


def test_photos_promoted_and_updated_on_conflict():
    """Фото должны доезжать из сырого слоя в listings и обновляться при
    повторной загрузке — иначе перезаливка оставит старый набор снимков."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings; DELETE FROM raw_listings;")
        conn.commit()
        row = {"external_id": "P1", "source": "cian", "price": 20_000_000,
               "area": 54.0, "kitchen_area": None, "rooms": 2, "level": 3,
               "levels": 9, "building_type": None, "object_type": None,
               "lat": 55.75, "lon": 37.62, "description": "с фото",
               "city": "msk", "address": "Москва", "source_url": None,
               "source_extra": {}, "photos": ["https://cdn/a.jpg"]}
        load_to_raw([row], conn)
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT photos FROM listings WHERE external_id='P1';")
            assert cur.fetchone()[0] == ["https://cdn/a.jpg"]

        load_to_raw([{**row, "photos": ["https://cdn/b.jpg", "https://cdn/c.jpg"]}], conn)
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT photos FROM listings WHERE external_id='P1';")
            assert cur.fetchone()[0] == ["https://cdn/b.jpg", "https://cdn/c.jpg"]
