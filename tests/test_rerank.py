from datetime import datetime, timezone
from habitus.online.rerank import rerank
from habitus.online.retrieval import Candidate


def _cand(eid: str, doc: str) -> Candidate:
    return Candidate(external_id=eid, doc_text=doc, price=None, area=None,
                     rooms=None, facts={}, score=0.0,
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
