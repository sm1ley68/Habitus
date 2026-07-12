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
