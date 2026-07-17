import json
import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import SPARSE_DIM, to_sparsevec_literal
from habitus.online.cache import embed_cache, explain_cache, parse_cache
from habitus.online.llm import FakeLLM, LLMResponse
from habitus.online.pipeline import run_search

DIM = 1024


@pytest.fixture(autouse=True)
def _clear_caches():
    for c in (embed_cache, parse_cache, explain_cache):
        c.clear()
    yield


def _vec(axis: int) -> str:
    v = [0.0] * DIM
    v[axis] = 1.0
    return "[" + ",".join(f"{x:g}" for x in v) + "]"


class FakeModel:
    """BGE-M3-заглушка: любой текст → ось 0 + токен 10 (матчит объект A)."""
    def encode(self, texts, **kw):
        dense = [0.0] * DIM
        dense[0] = 1.0
        return {"dense_vecs": [dense for _ in texts],
                "lexical_weights": [{"10": 1.0} for _ in texts]}


class BrokenModel:
    def encode(self, texts, **kw):
        raise RuntimeError("модель недоступна")


class FakeReranker:
    def compute_score(self, pairs, normalize=True, max_length=None):
        return [0.5] * len(pairs) if len(pairs) > 1 else 0.5


class BrokenReranker:
    def compute_score(self, pairs, normalize=True, max_length=None):
        raise RuntimeError("reranker упал")


def _parse_resp():
    return LLMResponse(content=None, tool_arguments=json.dumps(
        {"rooms": [2], "semantic_text": "тихо"}, ensure_ascii=False))


def _explain_resp():
    return LLMResponse(content="Тихая двушка, школа рядом.", tool_arguments=None)


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for eid, rooms, axis, tok in [("A", 2, 0, 10), ("B", 2, 1, 20)]:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, price,
                           rooms, area, noise_level, doc_text,
                           embedding, sparse_embedding)
                       VALUES (%s,'test',TRUE,10000000,%s,45,'low',%s,
                               %s::vector,%s::sparsevec);""",
                    (eid, rooms, f"объект {eid}", _vec(axis),
                     to_sparsevec_literal({tok: 1.0}, SPARSE_DIM)))
        c.commit()
        yield c


def test_happy_path_no_degradation(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    resp = run_search("тихая двушка", conn, llm=llm,
                      model=FakeModel(), reranker=FakeReranker())
    assert resp.degraded == []
    assert resp.results and resp.results[0].external_id in ("A", "B")
    assert resp.parsed.rooms == [2]
    assert resp.explanation == "Тихая двушка, школа рядом."
    assert resp.data_freshness.startswith("данные актуальны на ")


def test_no_llm_degrades_nlu_and_llm(conn):
    resp = run_search("тихая двушка", conn, llm=None,
                      model=FakeModel(), reranker=FakeReranker())
    assert "nlu" in resp.degraded and "llm" in resp.degraded
    assert resp.parsed.semantic_text == "тихая двушка"   # весь текст в семантику
    assert resp.results                                   # но поиск живой
    assert "Найдено объектов" in resp.explanation         # шаблон


def test_broken_encoder_degrades_vector_but_filters_work(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    resp = run_search("тихая двушка", conn, llm=llm,
                      model=BrokenModel(), reranker=FakeReranker())
    assert "vector" in resp.degraded
    assert {r.external_id for r in resp.results} == {"A", "B"}   # filter-only


def test_broken_reranker_keeps_rrf_order(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    resp = run_search("тихая двушка", conn, llm=llm,
                      model=FakeModel(), reranker=BrokenReranker())
    assert "reranker" in resp.degraded and resp.results


def test_parse_cache_hits_on_second_call(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp(), _explain_resp()])
    run_search("тихая двушка", conn, llm=llm, model=FakeModel(),
               reranker=FakeReranker())
    # второй прогон: parse из кэша → FakeLLM не должен исчерпаться
    resp2 = run_search("тихая двушка", conn, llm=llm, model=FakeModel(),
                       reranker=FakeReranker())
    assert resp2.parsed.rooms == [2]
    # объяснение тоже из кэша: третий _explain_resp не потрачен
    assert len(llm.responses) == 1
