# habitus/eval/runner.py — прогон golden-set: parse-accuracy + recall/NDCG
# с абляциями «dense-only vs RRF vs RRF+rerank» (слайд защиты)
import re
from pathlib import Path

import yaml

from habitus.config import settings
from habitus.eval.metrics import (ndcg_at_k, parse_accuracy, precision_at_k,
                                  recall_at_k, reciprocal_rank)
from habitus.online.llm import LLMClient, LLMUnavailable
from habitus.online.nlu import ParseError, parse_query
from habitus.online.rerank import prefilter_pool, proximity_rerank, rerank
from habitus.online.retrieval import hybrid_search
from habitus.online.schema import ParsedQuery

DEFAULT_GOLDEN = Path(__file__).parent / "queries.yaml"

VARIANTS = {"dense": ("dense",), "rrf": ("dense", "sparse")}
# абляции поверх RRF: чистый реранк, proximity-бленд, реранк+proximity
DERIVED = ("rrf+rerank", "rrf+prox", "rrf+rerank+prox")

# Вариант, по которому судим о готовности к продакшену: та же связка
# retrieval + реранк + proximity, что в online.pipeline.run_search. Не путь
# целиком — run_eval зовёт hybrid_search напрямую, без резолва области и без
# relaxation, поэтому меряет более трудную задачу, чем прод (см.
# docs/notes/eval-baseline-2026-08-18.md). Им же гейтит --check в cli.py.
GATE_VARIANT = "rrf+rerank+prox"


def load_golden(path: Path) -> list[dict]:
    with open(path, encoding="utf-8") as f:
        return yaml.safe_load(f)


def _avg(xs: list[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def series_of(query_id: str) -> str:
    """id запроса → буквенная серия ('a01' → 'a', 'c06' → 'c'). Серии в
    golden-set меряют разные стадии: a — структурные фильтры и proximity,
    c — текстовые оси (адрес/метро), решаемые только dense/sparse/реранком,
    d — сценарии домохозяйства (несколько мест семьи), которые меряют вклад
    household-сигнала в реранке.
    Смешивать их в одно число бессмысленно, поэтому run_eval считает и то,
    и другое отдельно (`by_series`), не только общий блендинг."""
    # только латиница: кириллическая «с» в id дала бы бакет, визуально
    # неотличимый от латинского «c» в таблице отчёта
    m = re.match(r"[a-z]+", query_id)
    return m.group(0) if m else query_id


def _aggregate(rows: list[dict]) -> dict[str, dict[str, float]]:
    """Список сырых замеров (по одному на пару запрос×вариант) → таблица
    вариант → {recall@10, precision@10, ndcg@10, mrr, n}, как в format_report."""
    out = {}
    for v in (*VARIANTS, *DERIVED):
        sub = [r for r in rows if r["variant"] == v]
        out[v] = {"recall@10": _avg([r["recall"] for r in sub]),
                  "precision@10": _avg([r["precision"] for r in sub]),
                  "ndcg@10": _avg([r["ndcg"] for r in sub]),
                  "mrr": _avg([r["mrr"] for r in sub]),
                  "n": len(sub)}
    return out


def run_eval(conn, llm: LLMClient | None, golden: list[dict],
             model=None, reranker=None, proximity_weight: float | None = None) -> dict:
    parse_scores: list[float] = []
    # Один плоский список сырых замеров (запрос×вариант) — источник и для общей
    # таблицы, и для разбивки по сериям (_aggregate фильтрует его дважды).
    rows: list[dict] = []

    def _score(variant: str, series: str, cands: list, relevant: set, rel_map: dict) -> None:
        ids = [c.external_id for c in cands]
        rows.append({"variant": variant, "series": series,
                    "recall": recall_at_k(relevant, ids),
                    "precision": precision_at_k(relevant, ids),
                    "ndcg": ndcg_at_k(rel_map, ids),
                    "mrr": reciprocal_rank(relevant, ids)})

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
        series = series_of(item["id"])
        rel_map = {k: float(v) for k, v in (item.get("relevance") or {}).items()} \
            or {r: 1.0 for r in relevant}
        pq = ParsedQuery.model_validate(
            {**expected, "semantic_text":
             expected.get("semantic_text") or item["query"]})
        # Точки домохозяйства берутся из queries.yaml явными координатами, а не
        # через геокодер: прогон обязан быть воспроизводимым и офлайновым.
        # В проде их резолвит pipeline.run_search через household_points().
        household = [(float(p[0]), float(p[1]))
                     for p in (item.get("household_points") or [])]
        rrf_cands = None
        for name, channels in VARIANTS.items():
            cands = hybrid_search(conn, pq, model=model, channels=channels,
                                  household=household)
            _score(name, series, cands, relevant, rel_map)
            if name == "rrf":
                rrf_cands = cands

        # proximity-бленд поверх RRF-скоров
        _score("rrf+prox", series,
               proximity_rerank(pq, rrf_cands, weight=proximity_weight,
                                household=household),
               relevant, rel_map)
        # тот же срез пула, что и в pipeline.run_search (prefilter_pool) — иначе
        # метрика меряет реранк по другому множеству кандидатов, чем отгружается
        pool = prefilter_pool(pq, rrf_cands)
        reranked_full = rerank(item["query"], pool, top_n=len(pool),
                               reranker=reranker)
        _score("rrf+rerank", series, reranked_full[: settings.rerank_top_n],
               relevant, rel_map)
        # proximity-бленд поверх скоров реранкера
        _score("rrf+rerank+prox", series,
               proximity_rerank(pq, reranked_full, weight=proximity_weight,
                                household=household),
               relevant, rel_map)

    return {
        "n_queries": len(golden),
        "parse_accuracy": _avg(parse_scores),
        "retrieval": _aggregate(rows),
        "by_series": {s: _aggregate([r for r in rows if r["series"] == s])
                     for s in sorted({r["series"] for r in rows})},
    }


def check_thresholds(res: dict, min_precision: float, min_ndcg: float,
                     variant: str = GATE_VARIANT) -> list[str]:
    """Сравнивает измеренные precision@10/NDCG@10 варианта `variant` (по
    умолчанию — продакшен-путь rrf+rerank+prox) с порогами гейта. Пустой
    список — гейт пройден; иначе человекочитаемые сообщения, какая метрика и
    насколько просела, для habitus/cli.py (`eval --check`).

    Гейт судит по precision, а не по recall: у запросов без ранжирующего
    сигнала релевантен весь пул (сотни объектов), их recall@10 упирается в
    10/|пул| и меняется от размера базы, а не от качества поиска. Precision@10
    от размера пула не зависит.
    """
    if not res["retrieval"][variant]["n"]:
        return [f"мерить нечего: ни одного запроса с эталоном для {variant}"]
    m = res["retrieval"][variant]
    failures = []
    if m["precision@10"] < min_precision:
        failures.append(
            f"precision@10 ({variant}) = {m['precision@10']:.3f}, порог "
            f"{min_precision:.3f} — просадка {min_precision - m['precision@10']:.3f}")
    if m["ndcg@10"] < min_ndcg:
        failures.append(
            f"NDCG@10 ({variant}) = {m['ndcg@10']:.3f}, порог {min_ndcg:.3f} "
            f"— просадка {min_ndcg - m['ndcg@10']:.3f}")
    return failures


def format_report(res: dict) -> str:
    lines = ["# Habitus eval", "",
             f"Запросов в golden-set: {res['n_queries']}",
             f"parse-accuracy: {res['parse_accuracy']:.2f}", "",
             "| вариант | recall@10 | precision@10 | NDCG@10 | MRR | n |",
             "|---|---|---|---|---|---|"]
    for name, m in res["retrieval"].items():
        lines.append(f"| {name} | {m['recall@10']:.2f} | "
                     f"{m['precision@10']:.2f} | "
                     f"{m['ndcg@10']:.2f} | {m['mrr']:.2f} | {m['n']} |")
    # По сериям — только тот же продакшен-вариант, иначе таблица не влезает и
    # дублирует то, что уже видно выше по каждой абляции.
    if res.get("by_series"):
        lines += ["", f"По сериям ({GATE_VARIANT}):", "",
                 "| серия | recall@10 | precision@10 | NDCG@10 | MRR | n |",
                 "|---|---|---|---|---|---|"]
        for series, agg in res["by_series"].items():
            m = agg[GATE_VARIANT]
            lines.append(f"| {series} | {m['recall@10']:.2f} | "
                         f"{m['precision@10']:.2f} | "
                         f"{m['ndcg@10']:.2f} | {m['mrr']:.2f} | {m['n']} |")
        lines += ["",
                  "recall@10 сопоставим только внутри серии: у запросов без "
                  "ранжирующего сигнала релевантен весь пул (сотни объектов), "
                  "поэтому их recall упирается в 10/|пул|. precision@10 от "
                  "размера пула не зависит — по нему и гейт."]
    return "\n".join(lines)
