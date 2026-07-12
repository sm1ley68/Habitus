import math
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
from habitus.eval.runner import DEFAULT_GOLDEN, format_report, load_golden, run_eval
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
    def compute_score(self, pairs, normalize=True):
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
    for variant in ("dense", "rrf", "rrf+rerank"):
        assert res["retrieval"][variant]["recall@10"] == 1.0
        assert res["retrieval"][variant]["ndcg@10"] == 1.0
    assert "rrf+rerank" in format_report(res)
