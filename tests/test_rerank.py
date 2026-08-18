from datetime import datetime, timezone
from habitus.online.rerank import prefilter_pool, rerank
from habitus.online.retrieval import Candidate
from habitus.online.schema import ParsedQuery


def _cand(eid: str, doc: str, facts: dict | None = None) -> Candidate:
    return Candidate(external_id=eid, doc_text=doc, price=None, area=None,
                     rooms=None, facts=facts or {}, score=0.0,
                     updated_at=datetime(2026, 7, 1, tzinfo=timezone.utc))


class FakeReranker:
    """Скорит по вхождению слова «школа» — детерминированно, без модели."""
    def __init__(self):
        self.pairs = None

    def compute_score(self, pairs, normalize=True, max_length=None):
        self.pairs = pairs
        return [0.9 if "школа" in doc else 0.1 for _, doc in pairs]


def test_rerank_orders_and_cuts_top_n():
    cands = [_cand("A", "просто квартира"), _cand("B", "школа рядом"),
             _cand("C", "ещё вариант")]
    fr = FakeReranker()
    out = rerank("школа в 10 минутах", cands, top_n=2, reranker=fr)
    assert [c.external_id for c in out] == ["B", "A"]   # tie A/C — стабильный порядок
    assert out[0].score == 0.9 and out[1].score == 0.1
    # пары (запрос, doc_text) ушли в реранкер
    assert fr.pairs[0] == ["школа в 10 минутах", "просто квартира"]


def test_rerank_single_candidate_scalar_score():
    class ScalarReranker:
        def compute_score(self, pairs, normalize=True, max_length=None):
            return 0.42          # FlagReranker для одной пары возвращает скаляр
    out = rerank("q", [_cand("A", "doc")], reranker=ScalarReranker())
    assert len(out) == 1 and out[0].score == 0.42


def test_rerank_empty_input():
    assert rerank("q", [], reranker=None) == []


def _geo_pq() -> ParsedQuery:
    return ParsedQuery.model_validate({"geo": [{"kind": "metro", "walk_minutes": 10}]})


def test_prefilter_pool_no_geo_truncates_by_rrf_order():
    cands = [_cand(str(i), f"doc{i}") for i in range(50)]
    out = prefilter_pool(ParsedQuery(semantic_text="q"), cands, pool_n=10)
    assert [c.external_id for c in out] == [str(i) for i in range(10)]


def test_prefilter_pool_geo_pulls_close_tail_candidate_keeps_rrf_head():
    # 50 кандидатов в RRF-порядке; только у "49" (хвост, за pool_n=10) есть
    # структурная близость — должен попасть в пул, не вытеснив голову RRF.
    cands = [_cand(str(i), f"doc{i}") for i in range(50)]
    cands[49] = _cand("49", "doc49", facts={"walk_min_metro": 1})
    out = prefilter_pool(_geo_pq(), cands, pool_n=10)
    ids = [c.external_id for c in out]
    assert ids[:5] == ["0", "1", "2", "3", "4"]   # RRF-голова (ceil(10/2)=5) сохранена
    assert "49" in ids


def test_prefilter_pool_missing_geo_data_does_not_crash_or_crowd_out():
    # 20 кандидатов без walk_min_*, кроме "15" (хвост, за pool_n=6) — с данными.
    pq = _geo_pq()
    cands = [_cand(str(i), f"doc{i}") for i in range(20)]
    cands[15] = _cand("15", "doc15", facts={"walk_min_metro": 2})
    out = prefilter_pool(pq, cands, pool_n=6)
    ids = [c.external_id for c in out]
    assert ids[:3] == ["0", "1", "2"]             # RRF-голова (ceil(6/2)=3) сохранена
    assert "15" in ids                            # единственный с данными — попал в пул


def test_prefilter_pool_short_input_returned_as_is():
    cands = [_cand(str(i), f"doc{i}") for i in range(5)]
    out = prefilter_pool(_geo_pq(), cands, pool_n=10)
    assert out is cands


def test_prefilter_pool_deterministic():
    pq = _geo_pq()
    cands = [_cand(str(i), f"doc{i}", facts={"walk_min_metro": i % 7})
             for i in range(30)]
    out1 = prefilter_pool(pq, cands, pool_n=10)
    out2 = prefilter_pool(pq, cands, pool_n=10)
    assert [c.external_id for c in out1] == [c.external_id for c in out2]
