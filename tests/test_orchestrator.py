from datetime import datetime, timezone
from habitus.online.orchestrator import relax, retrieve_with_relaxation
from habitus.online.retrieval import Candidate
from habitus.online.schema import GeoConstraint, ParsedQuery, PointConstraint


def _cand(eid: str) -> Candidate:
    return Candidate(external_id=eid, doc_text="d", price=None, area=None,
                     rooms=None, facts={}, score=0.1,
                     updated_at=datetime(2026, 7, 1, tzinfo=timezone.utc))


def test_relax_order_geo_price_orientation_noise():
    pq = ParsedQuery(geo=[GeoConstraint(kind="metro", walk_minutes=10)],
                     price_max=10_000_000, window_orientation=["SW"],
                     noise_max="low")
    pq, n1 = relax(pq)
    assert pq.geo[0].walk_minutes == 15 and "metro" in n1

    # гео на капе → следующий приоритет: бюджет
    pq = pq.model_copy(update={"geo": [GeoConstraint(kind="metro",
                                                     walk_minutes=30)]})
    pq, n2 = relax(pq)
    assert pq.price_max == 11_500_000 and "+15%" in n2

    # бюджет убрали → снимается ориентация окон
    pq = pq.model_copy(update={"price_max": None})
    pq, n3 = relax(pq)
    assert pq.window_orientation == [] and "окон" in n3

    pq, n4 = relax(pq)
    assert pq.noise_max is None and "шум" in n4

    assert relax(pq) is None            # ослаблять больше нечего


def test_relax_geo_capped_at_30():
    pq = ParsedQuery(geo=[GeoConstraint(kind="school", walk_minutes=28)])
    pq2, note = relax(pq)
    assert pq2.geo[0].walk_minutes == 30 and "28→30" in note
    # на капе гео-шаг больше не применяется, а других фильтров нет
    assert relax(pq2) is None


def test_relaxation_loop_stops_when_enough():
    calls = []

    def fake_search(conn, pq, **kw):
        calls.append(pq)
        return [_cand(f"id{i}") for i in range(5)]

    cands, relaxed, final = retrieve_with_relaxation(
        None, ParsedQuery(semantic_text="x"), search_fn=fake_search,
        min_results=5)
    assert len(calls) == 1 and relaxed == [] and len(cands) == 5


def test_relaxation_loop_widens_until_max_iters():
    pq = ParsedQuery(geo=[GeoConstraint(kind="school", walk_minutes=10)])
    seen = []

    def fake_search(conn, q, **kw):
        seen.append(q)
        return []

    cands, relaxed, final = retrieve_with_relaxation(
        None, pq, search_fn=fake_search, min_results=1, max_iters=2)
    # исходный вызов + 2 ослабления: 10→15, 15→20
    assert [q.geo[0].walk_minutes for q in seen] == [10, 15, 20]
    assert len(relaxed) == 2 and final.geo[0].walk_minutes == 20


def test_relaxation_loop_stops_when_nothing_to_relax():
    def fake_search(conn, q, **kw):
        return []

    cands, relaxed, final = retrieve_with_relaxation(
        None, ParsedQuery(semantic_text="только семантика"),
        search_fn=fake_search, min_results=1, max_iters=5)
    assert cands == [] and relaxed == []


def test_custom_point_builds_geo_predicate():
    captured = {}

    def fake_search(conn, q, *, geo_sql=None, geo_params=(), **kw):
        captured["geo_sql"] = geo_sql
        captured["geo_params"] = geo_params
        return [_cand("A")] * 5

    retrieve_with_relaxation(None, ParsedQuery(semantic_text="x"),
                             point=PointConstraint(lon=37.6, lat=55.7, minutes=10),
                             search_fn=fake_search, min_results=1)
    assert "ST_DWithin" in captured["geo_sql"]
    assert captured["geo_params"] == (37.6, 55.7, 800.0)   # 10 мин * 80 м
