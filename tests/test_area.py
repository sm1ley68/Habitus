# tests/test_area.py — область поиска: сторона города (округа) + именованное место
from pathlib import Path

import psycopg

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.zones import (
    backfill_listing_zones,
    import_admin_geojson,
    import_named_seed,
)
from habitus.online.geo import CARDINAL, AreaMatch, resolve_area

FIX = Path(__file__).parent / "fixtures" / "zones_sample.geojson"
SEED = Path(__file__).resolve().parents[1] / "data" / "named_zones.seed.json"


def _seeded_conn():
    c = psycopg.connect(settings.db_dsn)
    init_db(c)
    with c.cursor() as cur:
        cur.execute("TRUNCATE admin_zones, named_zones RESTART IDENTITY;")
    c.commit()
    import_admin_geojson(FIX, c)
    import_named_seed(SEED, c)
    backfill_listing_zones(c)
    return c


def test_cardinal_north_maps_to_three_okrugs():
    m = resolve_area("север")
    assert isinstance(m, AreaMatch)
    assert m.sql == "okrug = ANY(%s)"
    assert m.params == (["САО", "СВАО", "СЗАО"],)
    assert m.widen and m.widen[-1][0] == "TRUE"      # финальный шаг — снять область


def test_cardinal_diagonal_is_single_okrug():
    m = resolve_area("юго-запад москвы")
    assert m.params == (["ЮЗАО"],)


def test_center_maps_to_cao():
    m = resolve_area("в центре")
    assert m.params == (["ЦАО"],)
    assert "ЦАО" in m.label


def test_district_word_not_treated_as_cardinal_returns_none_without_conn():
    # «Северное Бутово» — не кардинал; без conn прочие ветки не отрабатывают → None
    assert resolve_area("Северное Бутово") is None


def test_empty_area_none():
    assert resolve_area("") is None


def test_named_zone_alias_to_dwithin():
    with _seeded_conn() as conn:
        m = resolve_area("Патрики", conn)
        assert "ST_DWithin" in m.sql
        assert m.params[0] == 37.5935 and m.params[1] == 55.7644  # якорь Патриарших
        assert "Патриаршие" in m.label


def test_district_name_to_column():
    with _seeded_conn() as conn:
        m = resolve_area("Хамовники", conn)
        assert m.sql == "raion = %s" and m.params == ("Хамовники",)
        # расширение: район → его округ → снять
        assert m.widen[0][0] == "okrug = %s" and m.widen[0][1] == ("ЦАО",)
        assert m.widen[-1][0] == "TRUE"


def test_okrug_name_to_column():
    with _seeded_conn() as conn:
        m = resolve_area("ЦАО", conn)
        assert m.sql == "okrug = %s" and m.params == ("ЦАО",)


def test_inside_sadovoe_uses_ring_polygon():
    with _seeded_conn() as conn:
        conn.execute("""INSERT INTO admin_zones (kind,name,geom) VALUES
            ('ring','Садовое кольцо', ST_Multi(ST_SetSRID(ST_MakePolygon(
             ST_GeomFromText('LINESTRING(37.60 55.75,37.64 55.75,37.64 55.77,37.60 55.77,37.60 55.75)')),4326)))""")
        conn.commit()
        m = resolve_area("внутри садового", conn)
        assert "ST_Within" in m.sql and "Садовое" in m.label
