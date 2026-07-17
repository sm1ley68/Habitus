# habitus/eval/runner.py — прогон golden-set: parse-accuracy + recall/NDCG
# с абляциями «dense-only vs RRF vs RRF+rerank» (слайд защиты)
from pathlib import Path

import yaml

from habitus.config import settings
from habitus.eval.metrics import ndcg_at_k, parse_accuracy, recall_at_k
from habitus.online.llm import LLMClient, LLMUnavailable
from habitus.online.nlu import ParseError, parse_query
from habitus.online.rerank import proximity_rerank, rerank
from habitus.online.retrieval import hybrid_search
from habitus.online.schema import ParsedQuery

DEFAULT_GOLDEN = Path(__file__).parent / "queries.yaml"

VARIANTS = {"dense": ("dense",), "rrf": ("dense", "sparse")}
# абляции поверх RRF: чистый реранк, proximity-бленд, реранк+proximity
DERIVED = ("rrf+rerank", "rrf+prox", "rrf+rerank+prox")


def load_golden(path: Path) -> list[dict]:
    with open(path, encoding="utf-8") as f:
        return yaml.safe_load(f)


def _avg(xs: list[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def run_eval(conn, llm: LLMClient | None, golden: list[dict],
             model=None, reranker=None, proximity_weight: float | None = None) -> dict:
    parse_scores: list[float] = []
    retr: dict[str, dict[str, list[float]]] = {
        v: {"recall": [], "ndcg": []} for v in (*VARIANTS, *DERIVED)}

    def _score(bucket: dict, cands: list, relevant: set, rel_map: dict) -> None:
        ids = [c.external_id for c in cands]
        bucket["recall"].append(recall_at_k(relevant, ids))
        bucket["ndcg"].append(ndcg_at_k(rel_map, ids))

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
            _score(retr[name], cands, relevant, rel_map)
            if name == "rrf":
                rrf_cands = cands

        # proximity-бленд поверх RRF-скоров
        _score(retr["rrf+prox"],
               proximity_rerank(pq, rrf_cands, weight=proximity_weight),
               relevant, rel_map)
        # реранк всего пула (скоры кросс-энкодера по всем кандидатам, потом срез)
        reranked_full = rerank(item["query"], rrf_cands, top_n=len(rrf_cands),
                               reranker=reranker)
        _score(retr["rrf+rerank"], reranked_full[: settings.rerank_top_n],
               relevant, rel_map)
        # proximity-бленд поверх скоров реранкера
        _score(retr["rrf+rerank+prox"],
               proximity_rerank(pq, reranked_full, weight=proximity_weight),
               relevant, rel_map)

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
