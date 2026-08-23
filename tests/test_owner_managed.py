import psycopg
from psycopg.types.json import Json

from habitus.config import settings
from habitus.clean.normalize import promote_to_listings
from habitus.db.init_db import init_db
from habitus.update.incremental import deactivate_missing

MSK = (37.62, 55.75)


def _fresh(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, raw_listings CASCADE;")
    conn.commit()


def _insert_owner(conn, external_id="owner_a1", price=9_000_000):
    with conn.cursor() as cur:
        cur.execute(
            """INSERT INTO listings (external_id, source, price, area, rooms,
                                     geom, city, owner_managed)
               VALUES (%s, 'owner', %s, 55.0, 2,
                       ST_SetSRID(ST_MakePoint(%s, %s), 4326), 'msk', true);""",
            (external_id, price, MSK[0], MSK[1]))
    conn.commit()


def test_deactivate_missing_spares_owner_managed():
    """Обход Циана не должен гасить объявление продавца: его нет и не может
    быть ни в одном снимке источника."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _fresh(conn)
        _insert_owner(conn)
        with conn.cursor() as cur:
            cur.execute(
                """INSERT INTO listings (external_id, source, price, area, rooms,
                                         geom, city)
                   VALUES ('cian_1', 'cian', 1e7, 40.0, 1,
                           ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'msk'),
                          ('cian_2', 'cian', 1e7, 41.0, 1,
                           ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'msk');""")
        conn.commit()

        deactivate_missing({"cian_1"}, conn, source="cian")

        with conn.cursor() as cur:
            cur.execute("SELECT external_id FROM listings WHERE is_active ORDER BY 1;")
            active = [r[0] for r in cur.fetchall()]
    assert active == ["cian_1", "owner_a1"]


def test_promote_does_not_overwrite_owner_edits():
    """Продавец привязал спарсенный объект и поправил цену. Следующий обход
    Циана приносит старую цену — правка должна пережить его."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _fresh(conn)
        with conn.cursor() as cur:
            cur.execute(
                """INSERT INTO listings (external_id, source, price, area, rooms,
                                         geom, city, owner_managed)
                   VALUES ('cian_777', 'cian', 12_000_000, 50.0, 2,
                           ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326), 'msk', true);""")
            cur.execute(
                """INSERT INTO raw_listings (external_id, source, price, area, rooms,
                                             lat, lon, city, source_extra)
                   VALUES ('cian_777', 'cian', 20_000_000, 50.0, 2,
                           55.7, 37.6, 'msk', %s);""", (Json({}),))
        conn.commit()

        promote_to_listings(conn)

        with conn.cursor() as cur:
            cur.execute("SELECT price FROM listings WHERE external_id='cian_777';")
            price = cur.fetchone()[0]
    assert price == 12_000_000
