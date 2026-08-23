import psycopg

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.document import refresh_doc_text
from habitus.geo.enrich import enrich_ids


def _two_listings(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings, poi CASCADE;")
        cur.execute("""
            INSERT INTO listings (external_id, source, price, area, rooms, geom, city, address)
            VALUES ('owner_scoped', 'owner', 1e7, 50.0, 2,
                    ST_SetSRID(ST_MakePoint(37.62, 55.75), 4326), 'msk', 'Москва, Тверская 1'),
                   ('cian_untouched', 'cian', 1e7, 60.0, 3,
                    ST_SetSRID(ST_MakePoint(37.63, 55.76), 4326), 'msk', 'Москва, Тверская 2');
            UPDATE listings SET updated_at = '2020-01-01', doc_text = NULL;
        """)
    conn.commit()


def test_enrich_ids_touches_only_requested_rows():
    """Точечное обогащение не должно переписывать всю таблицу: на 130k строк
    это минуты, а публикуется одно объявление."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _two_listings(conn)

        affected = enrich_ids(conn, ["owner_scoped"])

        with conn.cursor() as cur:
            cur.execute("""SELECT external_id, updated_at > '2021-01-01'
                           FROM listings ORDER BY external_id;""")
            touched = dict(cur.fetchall())
    assert affected == 1
    assert touched["owner_scoped"] is True
    assert touched["cian_untouched"] is False


def test_refresh_doc_text_scoped():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _two_listings(conn)

        refresh_doc_text(conn, ["owner_scoped"])

        with conn.cursor() as cur:
            cur.execute("""SELECT external_id, doc_text IS NOT NULL
                           FROM listings ORDER BY external_id;""")
            built = dict(cur.fetchall())
    assert built["owner_scoped"] is True
    assert built["cian_untouched"] is False


def test_refresh_doc_text_without_ids_covers_everything():
    """Батч-пайплайн зовёт без списка — поведение прежнее."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _two_listings(conn)

        count = refresh_doc_text(conn)

        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE doc_text IS NOT NULL;")
            built = cur.fetchone()[0]
    assert count == 2
    assert built == 2
