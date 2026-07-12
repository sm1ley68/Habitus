import pytest
from habitus.online.retrieval import build_where, rrf_merge
from habitus.online.schema import GeoConstraint, ParsedQuery


def test_rrf_merge_two_lists():
    merged = rrf_merge([["a", "b", "c"], ["b", "a"]], k=60)
    scores = dict(merged)
    assert scores["a"] == pytest.approx(1 / 61 + 1 / 62)
    assert scores["b"] == pytest.approx(1 / 62 + 1 / 61)
    assert scores["c"] == pytest.approx(1 / 63)
    assert merged[-1][0] == "c"                      # худший — только в одном списке


def test_rrf_merge_single_list_keeps_order():
    merged = rrf_merge([["x", "y"]], k=60)
    assert [eid for eid, _ in merged] == ["x", "y"]


def test_rrf_merge_tie_breaks_by_id():
    # одинаковые score → детерминированный порядок по external_id
    merged = rrf_merge([["b"], ["a"]], k=60)
    assert [eid for eid, _ in merged] == ["a", "b"]


def test_build_where_empty_query_only_active():
    sql, params = build_where(ParsedQuery())
    assert sql == "is_active = TRUE" and params == []


def test_build_where_full():
    pq = ParsedQuery(price_min=1, price_max=2, rooms=[1, 2], area_min=30.0,
                     area_max=60.0,
                     geo=[GeoConstraint(kind="school", walk_minutes=10),
                          GeoConstraint(kind="metro", walk_minutes=7)],
                     window_orientation=["SW", "W"], noise_max="medium",
                     stop_factors=["bars"], semantic_text="x")
    sql, params = build_where(pq)
    assert "price >= %s" in sql and "price <= %s" in sql
    assert "rooms = ANY(%s)" in sql
    assert "area >= %s" in sql and "area <= %s" in sql
    assert "walk_min_school <= %s" in sql and "walk_min_metro <= %s" in sql
    assert "noise_level = ANY(%s)" in sql
    assert "window_orientation && %s" in sql
    assert "bar_density_500m = 0" in sql
    # порядок параметров = порядку клауз
    assert params == [1, 2, [1, 2], 30.0, 60.0, 10, 7, ["low", "medium"], ["SW", "W"]]


def test_build_where_noise_high_means_no_filter():
    sql, _ = build_where(ParsedQuery(noise_max="high"))
    assert "noise_level" not in sql


def test_build_where_unknown_stop_factor_ignored():
    sql, _ = build_where(ParsedQuery(stop_factors=["communal_flats"]))
    assert "bar_density" not in sql            # колонки под это нет — молча пропускаем


def test_build_where_extra_geo_predicate():
    sql, params = build_where(ParsedQuery(), extra_sql="ST_DWithin(geom, %s, %s)",
                              extra_params=("PT", 500))
    assert sql.endswith("AND ST_DWithin(geom, %s, %s)")
    assert params == ["PT", 500]
