import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.retrieval import build_where, constraint_diagnostics, rrf_merge
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
    assert "bar_density_500m = 0" in sql
    # ориентация окон — не фильтр (данные есть у ~2% объявлений, см.
    # settings.orientation_weight): не должна ни попасть в WHERE, ни отъесть параметр
    assert "window_orientation" not in sql
    assert params == [1, 2, [1, 2], 30.0, 60.0, 10, 7, ["low", "medium"]]


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


def test_build_where_scopes_by_city():
    where, params = build_where(ParsedQuery(), city="msk")
    assert "city = %s" in where
    assert "msk" in params


def test_build_where_without_city_is_unscoped():
    where, _ = build_where(ParsedQuery())
    assert "city" not in where


# --- constraint_diagnostics (Task 6): диагностика пустой выдачи -------------

@pytest.fixture
def diag_conn():
    """Три объекта: A/B проходят по цене и комнатам, C — нет ни по цене, ни по
    комнатам, ни по шуму."""
    rows = [
        # (eid, price, rooms, area, walk_min_school, noise)
        ("A", 10_000_000, 2, 50.0, 8.0, "low"),
        ("B", 12_000_000, 2, 55.0, 9.0, "low"),
        ("C", 30_000_000, 3, 80.0, 25.0, "high"),
    ]
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for eid, price, rooms, area, ws, noise in rows:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, price,
                           rooms, area, walk_min_school, noise_level, doc_text)
                       VALUES (%s,'test',TRUE,%s,%s,%s,%s,%s,%s);""",
                    (eid, price, rooms, area, ws, noise, f"объект {eid}"))
        c.commit()
        yield c


def test_constraint_diagnostics_shows_killer_condition(diag_conn):
    # price_max=1 — ниже любой цены в фикстуре: убийственное условие обязано
    # обнулить remaining ровно на шаге «цена», а не раньше и не позже.
    pq = ParsedQuery(price_max=1, rooms=[2])
    diag = constraint_diagnostics(diag_conn, pq)
    by_label = {d["constraint"]: d["remaining"] for d in diag}
    assert by_label["база"] == 3
    assert by_label["цена"] == 0
    assert by_label["комнаты"] == 0     # цена уже обнулила — дальше остаётся 0


def test_constraint_diagnostics_no_constraints_returns_full_base(diag_conn):
    diag = constraint_diagnostics(diag_conn, ParsedQuery())
    assert diag == [{"constraint": "база", "remaining": 3}]


def test_constraint_diagnostics_step_order_matches_build_where(diag_conn):
    # Порядок шагов обязан совпадать с текущим build_where (Task 5: ориентация
    # окон в нём больше не клауза, поэтому в диагностике её тоже быть не должно).
    pq = ParsedQuery(price_min=1, price_max=50_000_000, rooms=[2, 3],
                     area_min=10.0, area_max=100.0,
                     geo=[GeoConstraint(kind="school", walk_minutes=30)],
                     window_orientation=["SW"], noise_max="medium",
                     stop_factors=["bars"])
    diag = constraint_diagnostics(diag_conn, pq, geo_sql="TRUE", geo_params=(),
                                  city=None)
    labels = [d["constraint"] for d in diag]
    assert labels == ["база", "цена", "комнаты", "площадь", "гео-минуты",
                      "шум", "стоп-факторы", "гео-предикат области"]
    remaining = [d["remaining"] for d in diag]
    assert remaining == sorted(remaining, reverse=True)   # накопительно не растёт
