import contextlib
import json

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


def _provider_seen(monkeypatch) -> dict:
    """Гоняет /search с подменённым run_search и возвращает увиденный provider."""
    fake_resp = SearchResponse(results=[], explanation="пусто",
                               parsed=ParsedQuery(), data_freshness="нет данных")
    seen = {}

    def fake_run_search(query, conn, **kw):
        seen["provider"] = kw.get("provider")
        return fake_resp

    monkeypatch.setattr(service, "run_search", fake_run_search)
    monkeypatch.setattr(service, "get_conn",
                        lambda: contextlib.nullcontext(None))
    r = TestClient(service.app).post("/search", json={"query": "тихо"})
    assert r.status_code == 200
    return seen


def test_search_endpoint_no_provider_when_key_empty(monkeypatch):
    # ors_base_url подменяется ЯВНО: без этого тест читал бы .env разработчика
    # и падал у того, кто поднял свой инстанс — «настроен ли ORS» определяется
    # парой (ключ, базовый URL), а не одним ключом (см. settings.ors_configured).
    monkeypatch.setattr(service.settings, "ors_api_key", "")
    monkeypatch.setattr(service.settings, "ors_base_url",
                        "https://api.openrouteservice.org")
    assert _provider_seen(monkeypatch)["provider"] is None


def test_search_endpoint_uses_own_instance_without_key(monkeypatch):
    # Свой инстанс ключа не требует вовсе — это и есть смысл ors_configured:
    # гейт по непустому ключу делал бы self-host невозможным без фиктивного
    # ORS_API_KEY (habitus/config.py, README «Свой OpenRouteService»).
    monkeypatch.setattr(service.settings, "ors_api_key", "")
    monkeypatch.setattr(service.settings, "ors_base_url", "http://ors:8082/ors")
    from habitus.online.geo import ORSProvider
    assert isinstance(_provider_seen(monkeypatch)["provider"], ORSProvider)


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


def test_search_endpoint_passes_explain_flag(monkeypatch):
    fake_resp = SearchResponse(results=[], explanation="", parsed=ParsedQuery(),
                               data_freshness="нет данных")
    seen = {}

    def fake_run_search(query, conn, **kw):
        seen["explain"] = kw.get("explain")
        return fake_resp

    monkeypatch.setattr(service, "run_search", fake_run_search)
    monkeypatch.setattr(service, "get_conn", lambda: contextlib.nullcontext(None))
    client = TestClient(service.app)

    client.post("/search", json={"query": "тихо", "explain": False})
    assert seen["explain"] is False

    client.post("/search", json={"query": "тихо"})
    assert seen["explain"] is True      # умолчание — прежнее поведение для CLI и eval


def _sse_frames(text: str) -> list[tuple[str, dict]]:
    frames = []
    for raw in text.split("\n\n"):
        if not raw.strip():
            continue
        event, data = "message", "{}"
        for line in raw.splitlines():
            if line.startswith("event:"):
                event = line[6:].strip()
            elif line.startswith("data:"):
                data = line[5:].strip()
        frames.append((event, json.loads(data)))
    return frames


def _explain_body():
    return {"query": "тихая двушка",
            "results": [{"external_id": "A", "price": 10000000, "area": 45.0,
                         "rooms": 2, "address_facts": {"walk_min_metro": 6.0},
                         "score": 0.9}],
            "relaxed": ["снят фильтр уровня шума"]}


def test_explain_stream_without_llm_key_streams_template(monkeypatch):
    monkeypatch.setattr(service.settings, "openrouter_api_key", "")
    r = TestClient(service.app).post("/explain/stream", json=_explain_body())

    assert r.status_code == 200
    assert r.headers["content-type"].startswith("text/event-stream")
    frames = _sse_frames(r.text)
    assert frames[-1] == ("done", {"llm_ok": False})
    text = "".join(d["token"] for e, d in frames if e == "token")
    assert "Найдено объектов: 1" in text            # факты запроса доехали
    assert "снят фильтр уровня шума" in text        # и ослабления тоже


def test_explain_stream_validates_empty_query():
    r = TestClient(service.app).post("/explain/stream", json={"query": ""})
    assert r.status_code == 422


def _tokens(frames):
    return "".join(d["token"] for e, d in frames if e == "token")


def test_explain_stream_reuses_cached_text_on_identical_request(monkeypatch):
    # Кэш объяснений жил в /search; при выносе стрима наружу он обязан
    # сохраниться, иначе каждый повтор запроса — новый платный вызов LLM.
    from habitus.online import llm as llm_mod
    from habitus.online.cache import explain_cache
    from habitus.online.llm import FakeStreamLLM

    explain_cache.clear()
    monkeypatch.setattr(service.settings, "openrouter_api_key", "ключ")
    monkeypatch.setattr(llm_mod, "AsyncOpenRouterLLM",
                        lambda: FakeStreamLLM(["Тихо ", "и близко."]))
    client = TestClient(service.app)

    first = _sse_frames(client.post("/explain/stream", json=_explain_body()).text)
    assert _tokens(first) == "Тихо и близко."
    assert first[-1] == ("done", {"llm_ok": True})

    def _no_llm():
        raise AssertionError("повторный запрос не должен ходить в LLM")

    monkeypatch.setattr(llm_mod, "AsyncOpenRouterLLM", _no_llm)
    second = _sse_frames(client.post("/explain/stream", json=_explain_body()).text)
    assert _tokens(second) == "Тихо и близко."
    assert second[-1] == ("done", {"llm_ok": True})


def test_explain_stream_does_not_cache_degraded_template(monkeypatch):
    # Шаблон — деградация, а не ответ: закэшировать его значит навсегда
    # подменить объяснение для этого запроса.
    from habitus.online import llm as llm_mod
    from habitus.online.cache import explain_cache
    from habitus.online.llm import FakeStreamLLM

    explain_cache.clear()
    monkeypatch.setattr(service.settings, "openrouter_api_key", "")
    client = TestClient(service.app)
    client.post("/explain/stream", json=_explain_body())

    monkeypatch.setattr(service.settings, "openrouter_api_key", "ключ")
    monkeypatch.setattr(llm_mod, "AsyncOpenRouterLLM",
                        lambda: FakeStreamLLM(["Живой ответ."]))
    frames = _sse_frames(client.post("/explain/stream", json=_explain_body()).text)

    assert _tokens(frames) == "Живой ответ."
    assert frames[-1] == ("done", {"llm_ok": True})
