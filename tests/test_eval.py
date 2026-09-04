import contextlib
import math
import sys

import pytest
from habitus.eval.metrics import ndcg_at_k, parse_accuracy, recall_at_k
from habitus.online.schema import ParsedQuery


def test_parse_accuracy_exact_and_partial():
    got = ParsedQuery(price_max=15_000_000, rooms=[2], noise_max="low")
    assert parse_accuracy({"price_max": 15_000_000, "rooms": [2]}, got) == 1.0
    assert parse_accuracy({"price_max": 15_000_000, "rooms": [3]}, got) == 0.5
    assert parse_accuracy({}, got) == 1.0


def test_parse_accuracy_geo_order_insensitive():
    got = ParsedQuery.model_validate(
        {"geo": [{"kind": "metro", "walk_minutes": 7},
                 {"kind": "school", "walk_minutes": 10}]})
    expected = {"geo": [{"kind": "school", "walk_minutes": 10},
                        {"kind": "metro", "walk_minutes": 7}]}
    assert parse_accuracy(expected, got) == 1.0


def test_recall_at_k():
    assert recall_at_k({"a", "b"}, ["a", "x", "b"], k=3) == 1.0
    assert recall_at_k({"a", "b"}, ["a", "x", "b"], k=2) == 0.5
    assert recall_at_k(set(), ["a"], k=10) == 1.0     # нет разметки — не штрафуем


def test_ndcg_at_k():
    assert ndcg_at_k({"a": 1.0}, ["a"], k=10) == 1.0
    assert ndcg_at_k({"a": 1.0}, ["x", "a"], k=10) == pytest.approx(1 / math.log2(3))
    assert ndcg_at_k({}, ["x"], k=10) == 0.0


import json
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import SPARSE_DIM, to_sparsevec_literal
from habitus.eval.runner import (DEFAULT_GOLDEN, check_thresholds, format_report,
                                 load_golden, run_eval, series_of)
from habitus.online.llm import FakeLLM, LLMResponse


def test_load_golden_default_file():
    golden = load_golden(DEFAULT_GOLDEN)
    assert len(golden) >= 10
    assert {"id", "query", "expected_parse"} <= set(golden[0].keys())
    assert any(g["lang"] == "en" for g in golden)


def test_run_eval_parse_only_no_db():
    golden = [{"id": "t1", "lang": "ru", "query": "двушка до 15 млн",
               "expected_parse": {"price_max": 15_000_000, "rooms": [2]},
               "relevant_ids": []}]
    fake = FakeLLM([LLMResponse(content=None, tool_arguments=json.dumps(
        {"price_max": 15_000_000, "rooms": [2]}))])
    res = run_eval(None, fake, golden)
    assert res["parse_accuracy"] == 1.0 and res["n_queries"] == 1
    report = format_report(res)
    assert "parse-accuracy" in report and "1.00" in report


class _EvalModel:
    """Кодирует любой запрос в ось 0 + токен 10 — детерминированный retrieval."""
    def encode(self, texts, **kw):
        dense = [0.0] * 1024
        dense[0] = 1.0
        return {"dense_vecs": [dense for _ in texts],
                "lexical_weights": [{"10": 1.0} for _ in texts]}


class _EvalReranker:
    def compute_score(self, pairs, normalize=True, max_length=None):
        s = [1.0 - i * 0.1 for i in range(len(pairs))]
        return s if len(s) > 1 else s[0]


def test_run_eval_retrieval_ablation():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            dense = [0.0] * 1024
            dense[0] = 1.0
            cur.execute(
                """INSERT INTO listings (external_id, source, is_active, price,
                       rooms, doc_text, embedding, sparse_embedding)
                   VALUES ('R1','test',TRUE,10000000,2,'тихая двушка',
                           %s::vector,%s::sparsevec);""",
                ("[" + ",".join(f"{x:g}" for x in dense) + "]",
                 to_sparsevec_literal({10: 1.0}, SPARSE_DIM)))
        conn.commit()
        golden = [{"id": "t2", "lang": "ru", "query": "тихая двушка",
                   "expected_parse": {"rooms": [2]},
                   "relevant_ids": ["R1"]}]
        fake = FakeLLM([LLMResponse(content=None,
                                    tool_arguments=json.dumps({"rooms": [2]}))])
        res = run_eval(conn, fake, golden, model=_EvalModel(),
                       reranker=_EvalReranker())
    for variant in ("dense", "rrf", "rrf+rerank", "rrf+prox", "rrf+rerank+prox"):
        assert res["retrieval"][variant]["recall@10"] == 1.0
        assert res["retrieval"][variant]["ndcg@10"] == 1.0
    assert "rrf+rerank+prox" in format_report(res)


def test_series_of_extracts_letter_prefix():
    assert series_of("a01") == "a"
    assert series_of("b06") == "b"
    assert series_of("c10") == "c"


def _thr(precision: float, ndcg: float, n: int = 1) -> dict:
    return {"precision@10": precision, "ndcg@10": ndcg, "n": n}


def test_check_thresholds_pass_and_fail():
    res = {"retrieval": {"rrf+rerank+prox": _thr(0.30, 0.28)}}
    assert check_thresholds(res, min_precision=0.25, min_ndcg=0.25) == []

    failures = check_thresholds(res, min_precision=0.40, min_ndcg=0.50)
    assert len(failures) == 2
    assert "precision@10" in failures[0] and "0.30" in failures[0] and "0.40" in failures[0]
    assert "NDCG@10" in failures[1] and "0.28" in failures[1] and "0.50" in failures[1]


def test_check_thresholds_uses_requested_variant():
    res = {"retrieval": {"dense": _thr(0.10, 0.10),
                        "rrf+rerank+prox": _thr(0.90, 0.90)}}
    # порог, который завалил бы "dense", не должен трогать выбранный вариант
    assert check_thresholds(res, min_precision=0.5, min_ndcg=0.5,
                            variant="rrf+rerank+prox") == []


def test_check_thresholds_reports_nothing_to_measure():
    """Пустой golden-set не должен читаться как «просадка до нуля»: гейт обязан
    сказать, что мерить нечего, иначе красный прогон уводит не туда."""
    res = {"retrieval": {"rrf+rerank+prox": _thr(0.0, 0.0, n=0)}}
    failures = check_thresholds(res, min_precision=0.29, min_ndcg=0.30)
    assert len(failures) == 1 and "мерить нечего" in failures[0]


def test_run_eval_by_series_breakdown_isolates_series():
    """Серии golden-set меряют разные стадии (a — структурные фильтры,
    c — текстовые оси) — разбивка по сериям обязана считаться отдельно, а не
    случайно усредняться в одну цифру вместе с общим `retrieval`."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            dense = [0.0] * 1024
            dense[0] = 1.0
            vec = "[" + ",".join(f"{x:g}" for x in dense) + "]"
            sparse = to_sparsevec_literal({10: 1.0}, SPARSE_DIM)
            for eid, rooms in (("R2", 2), ("R3", 3)):
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, price,
                           rooms, doc_text, embedding, sparse_embedding)
                       VALUES (%s,'test',TRUE,10000000,%s,'квартира',
                               %s::vector,%s::sparsevec);""",
                    (eid, rooms, vec, sparse))
        conn.commit()
        golden = [
            # a-серия: найдёт R2 (rooms=2) — recall 1.0 в своей серии
            {"id": "a1", "lang": "ru", "query": "двушка",
             "expected_parse": {"rooms": [2]}, "relevant_ids": ["R2"]},
            # c-серия: фильтр по rooms=3 отбирает только R3, но эталон указывает
            # на несуществующий объект — recall 0.0, специально, чтобы разбивка
            # по сериям была видна отдельно от a-серии, а не усреднена с ней
            {"id": "c1", "lang": "ru", "query": "трёшка",
             "expected_parse": {"rooms": [3]}, "relevant_ids": ["missing"]},
        ]
        res = run_eval(conn, None, golden, model=_EvalModel(), reranker=_EvalReranker())

    assert set(res["by_series"].keys()) == {"a", "c"}
    for variant in ("dense", "rrf", "rrf+rerank", "rrf+prox", "rrf+rerank+prox"):
        assert res["by_series"]["a"][variant]["recall@10"] == 1.0
        assert res["by_series"]["a"][variant]["n"] == 1
        assert res["by_series"]["c"][variant]["recall@10"] == 0.0
        assert res["by_series"]["c"][variant]["n"] == 1
        # общий retrieval — блендинг обеих серий, а не подмена одной другой
        assert res["retrieval"][variant]["recall@10"] == 0.5
        assert res["retrieval"][variant]["n"] == 2
    assert "По сериям" in format_report(res)


# --- гейт `eval --check`: код возврата (Task 9) -----------------------------

def _gate_report(precision: float, ndcg: float) -> dict:
    """Подставной отчёт run_eval — гейт проверяется без реального прогона."""
    return {"n_queries": 1, "parse_accuracy": 1.0,
            "retrieval": {"rrf+rerank+prox": {"recall@10": 0.5,
                                              "precision@10": precision,
                                              "ndcg@10": ndcg,
                                              "mrr": 0.5, "n": 1}},
            "by_series": {}}


def _run_gate(monkeypatch, report: dict, argv: list[str]) -> int | None:
    """Прогоняет `habitus eval --check` с подменённым run_eval и возвращает код
    выхода (None — вышли нормально). Проверяется именно связка «непустой список
    находок → ненулевой код», а не одна check_thresholds: без неё гейт молча
    превратился бы в печать текста."""
    import habitus.cli as cli
    from habitus.eval import runner

    monkeypatch.setattr(runner, "run_eval", lambda *a, **kw: report)
    monkeypatch.setattr(runner, "load_golden", lambda path: [])
    monkeypatch.setattr(cli, "get_conn", lambda: contextlib.nullcontext(None))
    monkeypatch.setattr(sys, "argv", ["habitus", *argv])
    try:
        cli.main()
    except SystemExit as exc:
        return exc.code
    return None


def test_check_exits_nonzero_below_threshold(monkeypatch, capsys):
    code = _run_gate(monkeypatch, _gate_report(0.10, 0.10),
                     ["eval", "--check", "--min-precision", "0.29",
                      "--min-ndcg", "0.30"])
    out = capsys.readouterr().out
    assert code == 1
    assert "ГЕЙТ НЕ ПРОЙДЕН" in out
    assert "precision@10" in out and "0.290" in out  # видно, какая метрика и порог


def test_check_exits_zero_above_threshold(monkeypatch, capsys):
    code = _run_gate(monkeypatch, _gate_report(0.40, 0.40),
                     ["eval", "--check", "--min-precision", "0.29",
                      "--min-ndcg", "0.30"])
    assert code is None                              # sys.exit не вызывался
    assert "гейт пройден" in capsys.readouterr().out


def test_without_check_flag_low_metrics_do_not_fail(monkeypatch, capsys):
    # Без --check прогон остаётся информационным: просадка печатается, но
    # код возврата нулевой — иначе обычный `habitus eval` ломал бы скрипты.
    code = _run_gate(monkeypatch, _gate_report(0.01, 0.01), ["eval"])
    assert code is None
    assert "ГЕЙТ НЕ ПРОЙДЕН" not in capsys.readouterr().out


# --- состояние данных в шапке отчёта ---------------------------------------
#
# База переливается launchd-агентом (scripts/refresh.sh) каждые 6 часов, и
# серия сдвигается от данных так же легко, как от правки кода. Пока состояние
# не печаталось, два прогона выглядели сравнимыми, не будучи ими: разбор
# просадки d-серии 4 сентября ушёл в поиск несуществующей регрессии.

def test_report_prints_dataset_state():
    from datetime import datetime
    from habitus.eval.runner import format_report
    res = {"n_queries": 5, "parse_accuracy": 0.9, "retrieval": {}, "by_series": {},
           "dataset": {"listings": 6945,
                       "updated_at": datetime(2026, 9, 4, 18, 8)}}
    out = format_report(res)
    assert "6945 объявлений" in out
    assert "2026-09-04 18:08" in out


def test_report_without_dataset_state_stays_valid():
    # conn=None (прогон без БД: только parse-accuracy) — строки о данных
    # просто нет, отчёт не падает и ничего не выдумывает.
    from habitus.eval.runner import format_report
    out = format_report({"n_queries": 5, "parse_accuracy": 0.9,
                         "retrieval": {}, "by_series": {}, "dataset": {}})
    assert "объявлений" not in out
    assert "# Habitus eval" in out
