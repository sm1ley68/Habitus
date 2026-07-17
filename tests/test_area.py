# tests/test_area.py — область поиска: сторона города (bbox) + место (геокод)
import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.geo import (AREA_RADIUS_M, CENTER_RADIUS_M, MOSCOW_CENTER,
                                resolve_area)
from habitus.online.orchestrator import retrieve_with_relaxation
from habitus.online.retrieval import filter_only_search
from habitus.online.schema import ParsedQuery

LON0, LAT0 = MOSCOW_CENTER


def _boom(_q):
    raise AssertionError("геокодер не должен вызываться для стороны света")


def test_cardinal_north_south_east_west():
    assert resolve_area("север", geocoder=_boom) == ("ST_Y(geom) >= %s", (LAT0,))
    assert resolve_area("на юге москвы", geocoder=_boom) == ("ST_Y(geom) <= %s", (LAT0,))
    assert resolve_area("западный", geocoder=_boom) == ("ST_X(geom) <= %s", (LON0,))
    assert resolve_area("восток", geocoder=_boom) == ("ST_X(geom) >= %s", (LON0,))


def test_cardinal_compound_southwest():
    sql, params = resolve_area("юго-запад", geocoder=_boom)
    assert sql == "ST_Y(geom) <= %s AND ST_X(geom) <= %s"
    assert params == (LAT0, LON0)


def test_cardinal_center_is_circle():
    sql, params = resolve_area("в центре", geocoder=_boom)
    assert "ST_DWithin" in sql
    assert params == (LON0, LAT0, CENTER_RADIUS_M)


def test_named_place_geocoded_to_proximity():
    calls = []
    def geo(q): calls.append(q); return (37.35, 55.70)   # (lon, lat)
    sql, params = resolve_area("Сколково", geocoder=geo)
    assert "ST_DWithin" in sql and params == (37.35, 55.70, AREA_RADIUS_M)
    assert calls == ["Сколково, Москва"]                  # добавили город


def test_district_with_direction_word_is_geocoded_not_cardinal():
    # «Северное Бутово» РЕАЛЬНО на юге — нельзя принять за «север»
    calls = []
    def geo(q): calls.append(q); return (37.55, 55.56)
    resolve_area("Северное Бутово", geocoder=geo)
    assert calls == ["Северное Бутово, Москва"]           # ушло в геокод


def test_geocode_failure_returns_none():
    assert resolve_area("Атлантида", geocoder=lambda q: None) is None


def test_empty_area_returns_none():
    assert resolve_area("", geocoder=_boom) is None


def test_orchestrator_applies_area_predicate():
    captured = {}
    def fake_search(conn, pq, **kw):
        captured.update(kw)
        return []
    retrieve_with_relaxation(None, ParsedQuery(area="север"),
                             geocoder=_boom, search_fn=fake_search, max_iters=0)
    assert "ST_Y(geom) >= %s" in captured["geo_sql"]
    assert list(captured["geo_params"]) == [LAT0]


def test_area_predicate_filters_in_postgis():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, geom) VALUES
                ('NORTH','test', ST_SetSRID(ST_MakePoint(37.60, 55.85), 4326)),
                ('SOUTH','test', ST_SetSRID(ST_MakePoint(37.60, 55.65), 4326));""")
        conn.commit()
        sql, params = resolve_area("север", geocoder=_boom)
        cands = filter_only_search(conn, ParsedQuery(), geo_sql=sql,
                                   geo_params=params)
        assert [c.external_id for c in cands] == ["NORTH"]
