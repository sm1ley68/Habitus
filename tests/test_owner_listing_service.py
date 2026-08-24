import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.owner_listing import (OwnerListingInvalid,
                                          upsert_owner_listing,
                                          withdraw_owner_listing)
from habitus.online.schema import OwnerListingUpsertRequest


class FakeModel:
    """Возвращает векторы нужной размерности, не поднимая BGE-M3."""

    def encode(self, texts, **kwargs):
        return {"dense_vecs": [[0.01] * 1024 for _ in texts],
                "lexical_weights": [{"1": 0.5} for _ in texts]}


def _req(**over) -> OwnerListingUpsertRequest:
    base = dict(external_id="owner_test1", source="owner", city="msk",
                price=12_000_000, area=54.0, kitchen_area=9.0, rooms=2,
                level=4, levels=17, address="Москва, улица Мельникова, 3к1",
                lng=37.6595, lat=55.7108, window_orientation=["юг"],
                description="Тихая двушка окнами во двор", photos=[],
                source_url="")
    base.update(over)
    return OwnerListingUpsertRequest(**base)


def _clean(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings CASCADE;")
    conn.commit()


def test_upsert_creates_indexed_owner_managed_row():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)

        indexed = upsert_owner_listing(_req(), conn, model=FakeModel())

        with conn.cursor() as cur:
            cur.execute("""SELECT source, owner_managed, is_active,
                                  doc_text IS NOT NULL, embedding IS NOT NULL,
                                  ST_X(geom), ST_Y(geom)
                           FROM listings WHERE external_id='owner_test1';""")
            row = cur.fetchone()
    assert indexed is True
    assert row[:5] == ("owner", True, True, True, True)
    assert round(row[5], 4) == 37.6595
    assert round(row[6], 4) == 55.7108


def test_upsert_is_idempotent_and_updates_price():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        upsert_owner_listing(_req(), conn, model=FakeModel())

        upsert_owner_listing(_req(price=11_000_000), conn, model=FakeModel())

        with conn.cursor() as cur:
            cur.execute("SELECT count(*), max(price) FROM listings WHERE external_id='owner_test1';")
            count, price = cur.fetchone()
    assert count == 1
    assert price == 11_000_000


def test_upsert_rejects_coordinates_of_another_city():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        with pytest.raises(OwnerListingInvalid) as exc:
            upsert_owner_listing(_req(city="spb"), conn, model=FakeModel())
    assert exc.value.field == "coordinates"


def test_upsert_rejects_absurd_price():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        with pytest.raises(OwnerListingInvalid) as exc:
            upsert_owner_listing(_req(price=1000), conn, model=FakeModel())
    assert exc.value.field == "price"


def test_withdraw_deactivates_without_deleting():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        upsert_owner_listing(_req(), conn, model=FakeModel())

        deactivated = withdraw_owner_listing("owner_test1", conn)

        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='owner_test1';")
            is_active = cur.fetchone()[0]
    assert deactivated is True
    assert is_active is False


def test_withdraw_unknown_id_is_not_an_error():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _clean(conn)
        assert withdraw_owner_listing("owner_nope", conn) is False
