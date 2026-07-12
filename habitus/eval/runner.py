# habitus/eval/runner.py — прогон golden-set: parse-accuracy + recall/NDCG
# с абляциями «dense-only vs RRF vs RRF+rerank» (слайд защиты)
from pathlib import Path

import yaml

from habitus.eval.metrics import ndcg_at_k, parse_accuracy, recall_at_k
from habitus.online.llm import LLMClient, LLMUnavailable
from habitus.online.nlu import ParseError, parse_query
from habitus.online.rerank import rerank
from habitus.online.retrieval import hybrid_search
from habitus.online.schema import ParsedQuery

DEFAULT_GOLDEN = Path(__file__).parent / "queries.yaml"

VARIANTS = {"dense": ("dense",), "rrf": ("dense", "sparse")}


def load_golden(path: Path) -> list[dict]:
    with open(path, encoding="utf-8") as f:
        return yaml.safe_load(f)


def _avg(xs: list[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def run_eval(conn, llm: LLMClient | None, golden: list[dict],
             model=None, reranker=None) -> dict:
    parse_scores: list[float] = []
    retr: dict[str, dict[str, list[float]]] = {
        v: {"recall": [], "ndcg": []} for v in (*VARIANTS, "rrf+rerank")}

    for item in golden:
        expected = item.get("expected_parse") or {}
        if llm is not None and expected:
            try:
                got = parse_query(item["query"], llm)
                parse_scores.append(parse_accuracy(expected, got))
            except (ParseError, LLMUnavailable):
                parse_scores.append(0.0)

        relevant = set(item.get("relevant_ids") or [])
        if not relevant or conn is None:
            continue
        rel_map = {k: float(v) for k, v in (item.get("relevance") or {}).items()} \
            or {r: 1.0 for r in relevant}
        pq = ParsedQuery.model_validate(
            {**expected, "semantic_text":
             expected.get("semantic_text") or item["query"]})
        rrf_cands = None
        for name, channels in VARIANTS.items():
            cands = hybrid_search(conn, pq, model=model, channels=channels)
            ids = [c.external_id for c in cands]
            retr[name]["recall"].append(recall_at_k(relevant, ids))
            retr[name]["ndcg"].append(ndcg_at_k(rel_map, ids))
            if name == "rrf":
                rrf_cands = cands
        reranked = rerank(item["query"], rrf_cands, reranker=reranker)
        ids = [c.external_id for c in reranked]
        retr["rrf+rerank"]["recall"].append(recall_at_k(relevant, ids))
        retr["rrf+rerank"]["ndcg"].append(ndcg_at_k(rel_map, ids))

    return {
        "n_queries": len(golden),
        "parse_accuracy": _avg(parse_scores),
        "retrieval": {v: {"recall@10": _avg(s["recall"]),
                          "ndcg@10": _avg(s["ndcg"]),
                          "n": len(s["recall"])}
                      for v, s in retr.items()},
    }


def format_report(res: dict) -> str:
    lines = ["# Habitus eval", "",
             f"Запросов в golden-set: {res['n_queries']}",
             f"parse-accuracy: {res['parse_accuracy']:.2f}", "",
             "| вариант | recall@10 | NDCG@10 | n |",
             "|---|---|---|---|"]
    for name, m in res["retrieval"].items():
        lines.append(f"| {name} | {m['recall@10']:.2f} | "
                     f"{m['ndcg@10']:.2f} | {m['n']} |")
    return "\n".join(lines)
