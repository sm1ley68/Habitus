# tests/test_proximity.py — proximity-rerank: блендинг близости с семантикой
from datetime import datetime, timezone

from habitus.online.rerank import proximity_rerank
from habitus.online.retrieval import Candidate
from habitus.online.schema import GeoConstraint, ParsedQuery


def _cand(eid: str, score: float, **facts) -> Candidate:
    return Candidate(external_id=eid, doc_text="d", price=None, area=None,
                     rooms=None, facts=facts, score=score,
                     updated_at=datetime(2026, 7, 1, tzinfo=timezone.utc))


def _metro_query() -> ParsedQuery:
    return ParsedQuery(geo=[GeoConstraint(kind="metro", walk_minutes=10)])


def test_empty_input():
    assert proximity_rerank(_metro_query(), []) == []


def test_no_geo_preserves_semantic_order_and_cuts_top_n():
    # без оси близости proximity-rerank не трогает порядок score, только срез
    cands = [_cand("A", 0.9), _cand("B", 0.5), _cand("C", 0.1)]
    out = proximity_rerank(ParsedQuery(), cands, weight=0.9, top_n=2)
    assert [c.external_id for c in out] == ["A", "B"]


def test_weight_zero_is_pure_semantic_order():
    pq = _metro_query()
    cands = [_cand("A", 0.9, walk_min_metro=25),   # семантика лучше, метро далеко
             _cand("B", 0.5, walk_min_metro=3)]    # семантика хуже, метро близко
    out = proximity_rerank(pq, cands, weight=0.0)
    assert [c.external_id for c in out] == ["A", "B"]


def test_weight_one_is_pure_proximity_order():
    pq = _metro_query()
    cands = [_cand("A", 0.9, walk_min_metro=25),
             _cand("B", 0.5, walk_min_metro=3)]
    out = proximity_rerank(pq, cands, weight=1.0)
    assert [c.external_id for c in out] == ["B", "A"]   # ближе к метро — выше


def test_blend_lifts_closer_candidate():
    pq = _metro_query()
    cands = [_cand("A", 0.9, walk_min_metro=25),
             _cand("B", 0.5, walk_min_metro=3)]
    # достаточный вес близости поднимает B над семантически «лучшим» A
    out = proximity_rerank(pq, cands, weight=0.7)
    assert [c.external_id for c in out] == ["B", "A"]
    assert out[0].score > out[1].score            # score перезаписан блендом


def test_multi_geo_sums_requested_axes():
    # как build_golden: composite = сумма walk_min по запрошенным осям
    pq = ParsedQuery(geo=[GeoConstraint(kind="school", walk_minutes=15),
                          GeoConstraint(kind="metro", walk_minutes=15)])
    cands = [_cand("A", 0.5, walk_min_school=5, walk_min_metro=5),   # сумма 10
             _cand("B", 0.5, walk_min_school=2, walk_min_metro=3)]   # сумма 5
    out = proximity_rerank(pq, cands, weight=1.0)
    assert [c.external_id for c in out] == ["B", "A"]


def test_missing_axis_fact_ranks_last():
    pq = _metro_query()
    cands = [_cand("A", 0.1, walk_min_metro=3),
             _cand("B", 0.9, walk_min_metro=None)]   # нет данных по оси → худшая близость
    out = proximity_rerank(pq, cands, weight=1.0)
    assert [c.external_id for c in out] == ["A", "B"]


def test_equal_scores_proximity_decides_no_crash():
    # вырожденный случай: все семантические score равны → решает близость
    pq = _metro_query()
    cands = [_cand("A", 0.5, walk_min_metro=20),
             _cand("B", 0.5, walk_min_metro=4)]
    out = proximity_rerank(pq, cands, weight=0.5)
    assert [c.external_id for c in out] == ["B", "A"]


def test_tie_break_by_external_id():
    # одинаковая близость и score → детерминированный порядок по id
    pq = _metro_query()
    cands = [_cand("B", 0.5, walk_min_metro=5),
             _cand("A", 0.5, walk_min_metro=5)]
    out = proximity_rerank(pq, cands, weight=0.5)
    assert [c.external_id for c in out] == ["A", "B"]
