import os
import psycopg
import pytest
from habitus.config import settings

pytestmark = pytest.mark.skipif(not os.environ.get("OPENROUTER_API_KEY"),
                                reason="нужен OPENROUTER_API_KEY")


def test_full_pipeline_live_qwen():
    """parse → retrieval → rerank → explain на реальном Qwen и реальной БД."""
    from habitus.online.llm import OpenRouterLLM
    from habitus.online.pipeline import run_search
    with psycopg.connect(settings.db_dsn) as conn:
        resp = run_search("тихая двушка до 15 млн рядом со школой, без баров",
                          conn, llm=OpenRouterLLM())
    assert "nlu" not in resp.degraded and "llm" not in resp.degraded
    assert resp.parsed.rooms == [2] and resp.parsed.stop_factors == ["bars"]
    assert resp.explanation.strip()


def test_english_query_live():
    from habitus.online.llm import OpenRouterLLM
    from habitus.online.nlu import parse_query
    pq = parse_query("quiet flat near a strong school", OpenRouterLLM())
    assert pq.lang == "en" and pq.noise_max == "low"
    assert any(g.kind == "school" for g in pq.geo)
