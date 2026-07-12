# habitus/online/rerank.py — bge-reranker-v2-m3, ленивая загрузка (как get_model в embed)
from dataclasses import replace
from habitus.config import settings
from habitus.online.retrieval import Candidate

_reranker = None


def get_reranker():
    global _reranker
    if _reranker is None:
        from FlagEmbedding import FlagReranker
        _reranker = FlagReranker(settings.reranker_model, use_fp16=True)
    return _reranker


def rerank(query: str, candidates: list[Candidate], top_n: int | None = None,
           reranker=None) -> list[Candidate]:
    """(запрос, doc_text) пары → скоры реранкера → top-N по убыванию."""
    if not candidates:
        return []
    n = top_n or settings.rerank_top_n
    r = reranker or get_reranker()
    scores = r.compute_score([[query, c.doc_text] for c in candidates],
                             normalize=True)
    if not isinstance(scores, list):        # одна пара → скаляр
        scores = [scores]
    ranked = sorted(zip(candidates, scores), key=lambda p: -p[1])
    return [replace(c, score=float(s)) for c, s in ranked[:n]]
