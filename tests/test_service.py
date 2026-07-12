import contextlib
from fastapi.testclient import TestClient
import habitus.online.service as service
from habitus.online.schema import ParsedQuery, SearchResponse


def test_health():
    client = TestClient(service.app)
    r = client.get("/health")
    assert r.status_code == 200 and r.json() == {"status": "ok"}


def test_search_endpoint_calls_pipeline(monkeypatch):
    fake_resp = SearchResponse(results=[], explanation="пусто",
                               parsed=ParsedQuery(), data_freshness="нет данных")
    seen = {}

    def fake_run_search(query, conn, **kw):
        seen["query"] = query
        return fake_resp

    monkeypatch.setattr(service, "run_search", fake_run_search)
    monkeypatch.setattr(service, "get_conn",
                        lambda: contextlib.nullcontext(None))
    r = TestClient(service.app).post("/search", json={"query": "тихо"})
    assert r.status_code == 200
    assert r.json()["explanation"] == "пусто" and seen["query"] == "тихо"


def test_search_endpoint_validates_input():
    r = TestClient(service.app).post("/search", json={"query": ""})
    assert r.status_code == 422
