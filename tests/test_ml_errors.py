import contextlib

import psycopg
import pytest
from fastapi.testclient import TestClient

import habitus.online.service as service
from habitus.config import settings
from habitus.online import trace
from habitus.online.errors import describe_failure
from habitus.online.llm import LLMUnavailable
from habitus.online.nlu import ParseError
from habitus.online.schema import ParsedQuery, SearchResponse


# --- trace: на какой стадии упало и что успело отработать до неё ---

def test_span_records_the_stage_it_failed_on():
    exc = RuntimeError("boom")
    with pytest.raises(RuntimeError):
        with trace.span("retrieval"):
            raise exc
    assert trace.failure_context(exc)["stage"] == "retrieval"


def test_innermost_stage_wins():
    """Виноват тот шаг, где рвануло, а не тот, что его обернул."""
    exc = RuntimeError("boom")
    with pytest.raises(RuntimeError):
        with trace.span("retrieval"):
            with trace.span("sql"):
                raise exc
    assert trace.failure_context(exc)["stage"] == "sql"


def test_with_timings_keeps_stages_measured_before_the_failure():
    @trace.with_timings
    def run():
        with trace.span("parse"):
            pass
        with trace.span("retrieval"):
            raise RuntimeError("boom")

    with pytest.raises(RuntimeError) as caught:
        run()
    ctx = trace.failure_context(caught.value)
    assert ctx["stage"] == "retrieval"
    # collector() сбрасывается на выходе из run_search — снимок таймингов
    # обязан сняться ДО этого, иначе причина «что успело» теряется.
    assert "parse" in ctx["timings"]


def test_span_still_measures_a_successful_run():
    """Диагностика отказа не должна ломать обычный сбор таймингов."""
    with trace.collector() as sink:
        with trace.span("parse"):
            pass
    assert "parse" in sink


# --- классификация: код, человеческая причина, что чинить ---

def test_database_down_is_named_as_such():
    d = describe_failure(psycopg.OperationalError("connection refused"))
    assert d["code"] == "db_unavailable"
    assert d["status"] == 503


def test_missing_table_points_at_the_offline_pipeline():
    d = describe_failure(psycopg.errors.UndefinedTable('relation "listings" does not exist'))
    assert d["code"] == "db_schema_missing"
    assert "habitus offline" in d["hint"]


def test_missing_postgis_function_is_not_confused_with_a_missing_table():
    d = describe_failure(psycopg.errors.UndefinedFunction("function st_dwithin does not exist"))
    assert d["code"] == "db_extension_missing"
    assert "PostGIS" in d["hint"] or "pgvector" in d["hint"]


def test_llm_outage_keeps_the_underlying_reason():
    d = describe_failure(LLMUnavailable("все модели цепочки недоступны: 429 rate limit"))
    assert d["code"] == "llm_unavailable"
    assert "429" in d["message"]


def test_nlu_parse_failure_has_its_own_code():
    d = describe_failure(ParseError("NLU: нет валидного ParsedQuery за 3 попытки"))
    assert d["code"] == "nlu_parse_failed"


def test_unknown_failure_names_the_exception_class():
    """Незнакомое исключение не должно схлопываться в пустое «внутренняя ошибка»."""
    d = describe_failure(ValueError("coordinates must be [lng, lat]"))
    assert d["code"] == "internal_error"
    assert "ValueError" in d["message"]
    assert "coordinates must be [lng, lat]" in d["message"]


def test_db_hint_names_the_host_but_never_the_password(monkeypatch):
    monkeypatch.setattr(settings, "db_dsn",
                        "postgresql://habitus:s3kret@db:5432/habitus")
    d = describe_failure(psycopg.OperationalError("connection to server failed"))
    assert "db:5432" in d["hint"]
    assert "s3kret" not in d["hint"] and "s3kret" not in d["message"]


def test_dsn_inside_the_exception_text_is_redacted():
    """psycopg охотно печатает DSN в тексте ошибки — пароль наружу не уходит."""
    exc = psycopg.OperationalError(
        'connection failed: "postgresql://habitus:s3kret@db:5432/habitus"')
    d = describe_failure(exc)
    assert "s3kret" not in d["message"]
    assert "habitus:***@db:5432" in d["message"]


def test_stage_and_timings_travel_from_trace_into_the_description():
    @trace.with_timings
    def run():
        with trace.span("rerank"):
            raise RuntimeError("boom")

    with pytest.raises(RuntimeError) as caught:
        run()
    d = describe_failure(caught.value)
    assert d["stage"] == "rerank"
    assert "rerank" in d["timings"]


# --- эндпоинт: структурный detail вместо "Internal Server Error" ---

def _client():
    return TestClient(service.app)


def test_search_failure_returns_structured_detail(monkeypatch):
    def boom(query, conn, **kw):
        raise LLMUnavailable("все модели цепочки недоступны: 429 rate limit")

    monkeypatch.setattr(service, "run_search", boom)
    monkeypatch.setattr(service, "get_conn", lambda: contextlib.nullcontext(None))
    r = _client().post("/search", json={"query": "тихо"})
    assert r.status_code == 503
    detail = r.json()["detail"]
    assert detail["code"] == "llm_unavailable"
    assert "429" in detail["message"]
    assert detail["hint"]


def test_search_failure_reports_the_stage_it_died_on(monkeypatch):
    def boom(query, conn, **kw):
        with trace.span("retrieval"):
            raise psycopg.errors.UndefinedTable('relation "listings" does not exist')

    monkeypatch.setattr(service, "run_search", boom)
    monkeypatch.setattr(service, "get_conn", lambda: contextlib.nullcontext(None))
    r = _client().post("/search", json={"query": "тихо"})
    assert r.status_code == 500
    detail = r.json()["detail"]
    assert detail["code"] == "db_schema_missing"
    assert detail["stage"] == "retrieval"


def test_search_failure_before_the_pipeline_is_still_structured(monkeypatch):
    """Падение на get_conn — стадии нет, но код и подсказка обязаны быть."""
    def no_db():
        raise psycopg.OperationalError("connection refused")

    monkeypatch.setattr(service, "get_conn", no_db)
    r = _client().post("/search", json={"query": "тихо"})
    assert r.status_code == 503
    assert r.json()["detail"]["code"] == "db_unavailable"


def test_successful_search_is_untouched(monkeypatch):
    fake = SearchResponse(results=[], explanation="пусто", parsed=ParsedQuery(),
                          data_freshness="нет данных")
    monkeypatch.setattr(service, "run_search", lambda query, conn, **kw: fake)
    monkeypatch.setattr(service, "get_conn", lambda: contextlib.nullcontext(None))
    r = _client().post("/search", json={"query": "тихо"})
    assert r.status_code == 200 and r.json()["explanation"] == "пусто"


def test_validation_error_is_not_swallowed_into_a_500():
    """422 от pydantic — не отказ пайплайна, обёртка не должна его перехватывать."""
    assert _client().post("/search", json={"query": ""}).status_code == 422


def test_dossier_not_found_keeps_its_404(monkeypatch):
    from habitus.online.dossier import DossierNotFound

    def boom(req, conn, route_provider=None):
        raise DossierNotFound("obj-1")

    monkeypatch.setattr(service, "build_dossier", boom)
    monkeypatch.setattr(service, "get_conn", lambda: contextlib.nullcontext(None))
    r = _client().post("/dossier", json={"object_id": "obj-1", "city": "msk"})
    assert r.status_code == 404
