import contextlib
from fastapi.testclient import TestClient
import habitus.online.service as service
from habitus.online.schema import (DossierPayload, ParsedQuery, SearchResponse,
                                   VerdictInfo)


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


def test_search_endpoint_injects_ors_provider_when_key_set(monkeypatch):
    from habitus.online.geo import ORSProvider

    fake_resp = SearchResponse(results=[], explanation="пусто",
                               parsed=ParsedQuery(), data_freshness="нет данных")
    seen = {}

    def fake_run_search(query, conn, **kw):
        seen["provider"] = kw.get("provider")
        return fake_resp

    monkeypatch.setattr(service.settings, "ors_api_key", "test-key")
    monkeypatch.setattr(service, "run_search", fake_run_search)
    monkeypatch.setattr(service, "get_conn",
                        lambda: contextlib.nullcontext(None))
    r = TestClient(service.app).post("/search", json={"query": "тихо"})
    assert r.status_code == 200
    assert isinstance(seen["provider"], ORSProvider)


def test_search_endpoint_no_provider_when_key_empty(monkeypatch):
    fake_resp = SearchResponse(results=[], explanation="пусто",
                               parsed=ParsedQuery(), data_freshness="нет данных")
    seen = {}

    def fake_run_search(query, conn, **kw):
        seen["provider"] = kw.get("provider")
        return fake_resp

    monkeypatch.setattr(service.settings, "ors_api_key", "")
    monkeypatch.setattr(service, "run_search", fake_run_search)
    monkeypatch.setattr(service, "get_conn",
                        lambda: contextlib.nullcontext(None))
    r = TestClient(service.app).post("/search", json={"query": "тихо"})
    assert r.status_code == 200
    assert seen["provider"] is None


def test_dossier_endpoint_returns_versioned_payload(monkeypatch):
    payload = DossierPayload(
        verdict=VerdictInfo(headline="Недостаточно данных", confidence=0,
                            layers_checked=0),
        brief=[], blocks=[], compromises=[], relaxation=[], zone_rationale="",
    )
    monkeypatch.setattr(service.settings, "ors_api_key", "")
    monkeypatch.setattr(service, "get_conn",
                        lambda: contextlib.nullcontext(None))
    monkeypatch.setattr(service, "build_dossier", lambda req, conn, **kw: payload)
    response = TestClient(service.app).post("/dossier", json={"object_id": "E1"})
    assert response.status_code == 200
    assert response.json()["schema_version"] == "dossier-v1"
    assert response.json()["dossier"]["brief"] == []


def test_startup_ensures_dossier_schema(monkeypatch):
    # Регресс: без init_db на старте /dossier падает с UndefinedTable (500) на
    # БД, где ещё не гоняли import-evidence/import-osm-features. lifespan обязан
    # идемпотентно создать схему до приёма трафика.
    calls = []
    monkeypatch.setattr(service, "init_db", lambda conn: calls.append(conn))
    monkeypatch.setattr(service, "get_conn",
                        lambda: contextlib.nullcontext("conn"))
    with TestClient(service.app):
        pass
    assert calls == ["conn"]


def test_object_ask_without_llm_returns_grounded_unknown(monkeypatch):
    monkeypatch.setattr(service.settings, "openrouter_api_key", "")
    response = TestClient(service.app).post("/object-ask", json={
        "question": "Что неизвестно?", "passport": {"id": "E1"},
    })
    assert response.status_code == 200
    assert response.json()["sentences"][0]["unknown"] is True
