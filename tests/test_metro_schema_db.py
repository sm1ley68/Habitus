import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db

EXPECTED = {
    "metro_line": {"id", "city", "system", "ref", "name", "colour",
                   "headway_s", "headway_estimated", "fallback_speed_kmh",
                   "updated_at"},
    "metro_station": {"id", "city", "line_id", "osm_id", "name", "name_norm",
                      "geom", "order_index", "updated_at"},
    "metro_edge": {"city", "from_station", "to_station", "seconds", "estimated"},
    "metro_transfer": {"city", "from_station", "to_station", "seconds",
                       "estimated", "outdoor"},
    "metro_line_geom": {"line_id", "geom"},
    "listing_metro_access": {"external_id", "station_id", "walk_seconds",
                             "estimated", "updated_at"},
}


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        yield c


def _columns(conn, table: str) -> set[str]:
    rows = conn.execute(
        "SELECT column_name FROM information_schema.columns WHERE table_name = %s",
        (table,)).fetchall()
    return {r[0] for r in rows}


@pytest.mark.parametrize("table,cols", EXPECTED.items())
def test_table_has_expected_columns(conn, table, cols):
    assert cols <= _columns(conn, table), f"{table}: не хватает колонок"


def test_init_db_is_idempotent(conn):
    # схема применяется поверх себя без ошибок — иначе повторный offline упадёт
    init_db(conn)
    assert _columns(conn, "metro_line")


def test_station_geom_is_indexed(conn):
    rows = conn.execute(
        "SELECT indexdef FROM pg_indexes WHERE tablename = 'metro_station'"
    ).fetchall()
    assert any("gist" in r[0].lower() for r in rows), "нет GIST по geom"
