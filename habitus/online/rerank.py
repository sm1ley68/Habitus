# habitus/online/rerank.py — bge-reranker-v2-m3, ленивая загрузка (как get_model в embed)
from dataclasses import replace

from habitus.config import settings
from habitus.online.retrieval import Candidate
from habitus.online.schema import ParsedQuery

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
                             normalize=True, max_length=settings.rerank_max_length)
    if not isinstance(scores, list):        # одна пара → скаляр
        scores = [scores]
    ranked = sorted(zip(candidates, scores), key=lambda p: -p[1])
    return [replace(c, score=float(s)) for c, s in ranked[:n]]


def _minmax(values: list[float]) -> list[float]:
    """min-max в [0,1]. Вырожденный диапазон (все равны) → константа 0.0
    (не влияет на порядок — решает другой сигнал бленда)."""
    lo, hi = min(values), max(values)
    if hi == lo:
        return [0.0] * len(values)
    return [(v - lo) / (hi - lo) for v in values]


def _proximity_raw(pq: ParsedQuery, c: Candidate) -> float | None:
    """Composite близости = сумма walk_min по ОСЯМ, которые запрос явно попросил
    (как order_sql в build_golden). None, если хоть по одной оси нет данных."""
    vals = [c.facts.get(f"walk_min_{g.kind}") for g in pq.geo]
    if not vals or any(v is None for v in vals):
        return None
    return float(sum(vals))


def proximity_rerank(pq: ParsedQuery, candidates: list[Candidate], *,
                     weight: float | None = None,
                     top_n: int | None = None) -> list[Candidate]:
    """Блендинг структурного сигнала точной близости с семантическим score.

    Cross-encoder-реранкер слеп к точным минутам (в doc_text они — крошечный хвост
    на фоне ~1600 символов прозы). Здесь среди уже отфильтрованных и семантически
    отранжированных кандидатов подмешиваем нормированную близость по осям запроса:
        blended = weight * proximity_norm + (1 - weight) * score_norm
    weight — доля близости (`settings.proximity_weight`). Это бленд, а не сортировка
    по оси: семантика сохраняет вес, поэтому метрика меряет реальное улучшение
    ранжирования, а не тавтологию «отсортировали ровно по тому, чем метили golden».
    """
    if not candidates:
        return []
    n = top_n or settings.rerank_top_n
    w = settings.proximity_weight if weight is None else weight
    # нет оси близости в запросе или нулевой вес → близость не при чём:
    # сохраняем входной порядок (RRF / реранкер / свежесть), только срез top-N
    if not pq.geo or w <= 0.0:
        return candidates[:n]

    raws = [_proximity_raw(pq, c) for c in candidates]
    known = [r for r in raws if r is not None]
    if known:
        kmin, kmax = min(known), max(known)
        span = kmax - kmin
        # ближе (меньше минут) → выше; отсутствие данных по оси → худшая близость 0
        prox_norm = [0.0 if r is None else
                     (1.0 if span == 0 else 1.0 - (r - kmin) / span) for r in raws]
    else:
        prox_norm = [0.0] * len(candidates)     # ни по кому нет данных → сигнала нет
    score_norm = _minmax([c.score for c in candidates])

    blended = [w * p + (1.0 - w) * s for p, s in zip(prox_norm, score_norm)]
    order = sorted(zip(candidates, blended),
                   key=lambda cb: (-cb[1], cb[0].external_id))
    return [replace(c, score=float(b)) for c, b in order[:n]]
