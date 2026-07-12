# habitus/eval/metrics.py — parse-accuracy, recall@10, NDCG@10
import math

from habitus.online.schema import ParsedQuery


def _norm(v):
    """Нормализация для сравнения: списки скаляров сортируем,
    списки dict (geo) сортируем по каноничному представлению."""
    if isinstance(v, list):
        if v and isinstance(v[0], dict):
            return sorted((sorted(d.items()) for d in v))
        return sorted(v)
    return v


def parse_accuracy(expected: dict, got: ParsedQuery) -> float:
    """Доля полей эталона, совпавших с фактическим парсом (field-level)."""
    if not expected:
        return 1.0
    dump = got.model_dump()
    hits = sum(1 for k, v in expected.items() if _norm(dump.get(k)) == _norm(v))
    return hits / len(expected)


def recall_at_k(relevant: set[str], got: list[str], k: int = 10) -> float:
    if not relevant:
        return 1.0
    return len(relevant & set(got[:k])) / len(relevant)


def ndcg_at_k(relevance: dict[str, float], got: list[str], k: int = 10) -> float:
    dcg = sum(relevance.get(x, 0.0) / math.log2(i + 2)
              for i, x in enumerate(got[:k]))
    ideal = sorted(relevance.values(), reverse=True)[:k]
    idcg = sum(r / math.log2(i + 2) for i, r in enumerate(ideal))
    return dcg / idcg if idcg else 0.0
