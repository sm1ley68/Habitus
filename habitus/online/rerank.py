# habitus/online/rerank.py — bge-reranker-v2-m3, ленивая загрузка (как get_model в embed)
from dataclasses import replace
from habitus.config import settings
from habitus.online.retrieval import Candidate

_reranker = None


def get_reranker():
    global _reranker
    if _reranker is None:
        import torch
        from FlagEmbedding import FlagReranker
        # На Apple MPS кросс-энкодер реранкера непригоден: с fp16 роняет forward на
        # длинных документах, а без fp16 — зависает на MPS-устройстве (замерено:
        # 50 пар > 10 мин против 176 c на CPU). Поэтому вне CUDA пинуем на CPU.
        # fp16 стабилен и выгоден только на CUDA. В проде (Linux/CPU) — тоже CPU.
        cuda = torch.cuda.is_available()
        _reranker = FlagReranker(settings.reranker_model, use_fp16=cuda,
                                 devices=None if cuda else "cpu")
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
