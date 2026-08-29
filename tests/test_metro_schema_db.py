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


def _column_type(conn, table: str, column: str) -> tuple[str, str]:
    """(data_type, is_nullable) для одной колонки — типы, не только имена."""
    row = conn.execute(
        "SELECT data_type, is_nullable FROM information_schema.columns "
        "WHERE table_name = %s AND column_name = %s",
        (table, column)).fetchone()
    assert row is not None, f"{table}.{column}: колонка не найдена"
    return row[0], row[1]


def _geometry_column(conn, table: str, column: str) -> tuple[int, str]:
    """(srid, type) геометрической колонки по каталогу PostGIS geometry_columns."""
    row = conn.execute(
        "SELECT srid, type FROM geometry_columns "
        "WHERE f_table_name = %s AND f_geometry_column = %s",
        (table, column)).fetchone()
    assert row is not None, f"{table}.{column}: не зарегистрирована в geometry_columns"
    return row[0], row[1]


@pytest.mark.parametrize("table,cols", EXPECTED.items())
def test_table_has_expected_columns(conn, table, cols):
    # Подмножество (<=), не точное равенство: Задачи 6/7/9/12 законно добавят
    # свои колонки в эти таблицы (например служебные поля инкрементальной
    # загрузки), и точное совпадение превращало бы каждое такое добавление в
    # правку этого теста без сигнала об ошибке. Инвариант, который важно не
    # ослаблять случайно, — типы и nullability конкретных колонок (ниже),
    # а не закрытый список всех колонок таблицы.
    assert cols <= _columns(conn, table), f"{table}: не хватает колонок"


# Колонки-признаки происхождения времени: NOT NULL boolean. Именно они несут
# инвариант «курировано vs оценено» до фронта (см. CLAUDE.md) — тихая потеря
# NOT NULL здесь означала бы, что оценка когда-нибудь останется без пометки.
ESTIMATED_COLUMNS = [
    ("metro_edge", "estimated"),
    ("metro_transfer", "estimated"),
    ("listing_metro_access", "estimated"),
    ("metro_line", "headway_estimated"),
]

# Длительности, которые сопровождают признаки выше: INTEGER NOT NULL. Если
# любая из них тихо станет TEXT/REAL или nullable, движок графа (Задача 9)
# получит либо мусор для арифметики, либо синтетическую дыру вместо замера.
DURATION_COLUMNS = [
    ("metro_edge", "seconds"),
    ("metro_transfer", "seconds"),
    ("listing_metro_access", "walk_seconds"),
    ("metro_line", "headway_s"),
]

# (таблица, колонка, тип геометрии) — оба геометрических столбца обязаны
# остаться в WGS84/4326 (см. CLAUDE.md «координаты везде [lng, lat], WGS84»)
# и сохранить свой тип: точка для станции, линия для геометрии маршрута.
GEOMETRY_COLUMNS = [
    ("metro_station", "geom", "POINT"),
    ("metro_line_geom", "geom", "LINESTRING"),
]


@pytest.mark.parametrize("table,column", ESTIMATED_COLUMNS)
def test_estimated_columns_are_boolean_not_null(conn, table, column):
    data_type, is_nullable = _column_type(conn, table, column)
    assert data_type == "boolean", f"{table}.{column}: тип {data_type}, ожидался boolean"
    assert is_nullable == "NO", f"{table}.{column}: колонка nullable, должна быть NOT NULL"


@pytest.mark.parametrize("table,column", DURATION_COLUMNS)
def test_duration_columns_are_integer_not_null(conn, table, column):
    data_type, is_nullable = _column_type(conn, table, column)
    assert data_type == "integer", f"{table}.{column}: тип {data_type}, ожидался integer"
    assert is_nullable == "NO", f"{table}.{column}: колонка nullable, должна быть NOT NULL"


@pytest.mark.parametrize("table,column,geom_type", GEOMETRY_COLUMNS)
def test_geometry_columns_have_srid_4326_and_expected_type(conn, table, column, geom_type):
    srid, actual_type = _geometry_column(conn, table, column)
    assert srid == 4326, f"{table}.{column}: srid {srid}, ожидался 4326 (WGS84)"
    assert actual_type == geom_type, f"{table}.{column}: тип геометрии {actual_type}, ожидался {geom_type}"


def test_init_db_is_idempotent(conn):
    # схема применяется поверх себя без ошибок — иначе повторный offline упадёт
    init_db(conn)
    assert _columns(conn, "metro_line")


def test_station_geom_is_indexed(conn):
    rows = conn.execute(
        "SELECT indexdef FROM pg_indexes WHERE tablename = 'metro_station'"
    ).fetchall()
    assert any("gist" in r[0].lower() for r in rows), "нет GIST по geom"
