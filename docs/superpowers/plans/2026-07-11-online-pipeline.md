# Online-фаза (Фаза 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Превратить свободный текст пользователя в ранжированный список объектов недвижимости + grounded-объяснение — библиотека `habitus/online/*`, тонкий FastAPI `POST /search`, eval-харнесс с golden-set.

**Architecture:** Пайплайн NLU (LLM function-calling → `ParsedQuery`) → оркестратор с relaxation-петлёй → гибридный retrieval (SQL WHERE + dense + sparse + RRF по готовой БД offline-фазы) → reranker bge-reranker-v2-m3 → объяснение строго поверх фактов из БД. Каждый слой умеет деградировать, не убивая систему; всё собирается в `pipeline.run_search()`, поверх — тонкий FastAPI и CLI.

**Tech Stack:** Python 3.12 / uv, psycopg3, PostgreSQL + PostGIS + pgvector (dev-БД порт 5544), FlagEmbedding (BGE-M3, FlagReranker), openai-SDK → OpenRouter (Qwen + фолбэки), FastAPI/uvicorn, pydantic v2, pytest.

**Спека:** `docs/superpowers/specs/2026-07-11-online-pipeline-design.md` — источник правды. 9 задач = 9 блоков раздела 7.

## Global Constraints

- Python 3.12, пакетный менеджер `uv` (`uv run pytest`, `uv add <pkg>`).
- Эмбеддинги: BGE-M3, dense `vector(1024)`, sparse `sparsevec(250002)` (`SPARSE_DIM = 250002` из `habitus/embed/encode.py`). Модель НЕ обучается — только инференс готовых моделей.
- RRF: `score = Σ 1/(60+rank)`, `k=60` конфигурируем через `settings.rrf_k`.
- Все LLM-вызовы: `temperature=0`.
- Dev-БД: `postgresql://habitus:habitus@localhost:5544/habitus` (порт **5544**, `settings.db_dsn`). Тесты с БД подключаются напрямую по `settings.db_dsn` + `init_db(conn)` + `TRUNCATE` — как существующие `tests/test_incremental.py`.
- Reranker: `BAAI/bge-reranker-v2-m3` через `FlagReranker` (уже в зависимости `FlagEmbedding`).
- LLM: Qwen через OpenRouter (openai-SDK, `base_url=https://openrouter.ai/api/v1`), фолбэк-цепочка DeepSeek → GPT-4o-mini при ошибке/таймауте.
- Границы зон: ML/Data — наша (`habitus/online/*`, `habitus/eval/*`, тонкий FastAPI `service.py`). Беки владеют gateway (Go/Fiber), деплоем, инфраструктурой БД-сервера, транспортом Go↔Python — НЕ трогаем. Схему БД (`listings`, `poi`) структурно НЕ меняем.
- Коммиты: сообщения на русском, БЕЗ упоминания Claude/AI, БЕЗ трейлеров Co-Authored-By.
- Стиль кода: как в offline-фазе — короткие модули, комментарии на русском, ленивая загрузка тяжёлых моделей (`get_model()`-паттерн), конфиг только через `habitus.config.settings`.
- Никаких плейсхолдеров в коде; типы и сигнатуры между задачами — ровно как в блоках Interfaces.

---

### Task 1: Фундамент — config, схемы контракта, LLM-клиент

**Files:**
- Modify: `habitus/config.py`
- Modify: `pyproject.toml` (добавить `openai`)
- Create: `habitus/online/__init__.py` (пустой)
- Create: `habitus/online/schema.py`
- Create: `habitus/online/llm.py`
- Test: `tests/test_online_schema.py`
- Test: `tests/test_llm.py`

**Interfaces:**
- Consumes: `habitus.config.settings` (существующий pydantic-settings синглтон).
- Produces:
  - `schema.py`: `GeoConstraint(kind: Literal["school","metro","park"], walk_minutes: int)`, `ParsedQuery`, `ResultItem`, `SearchResponse`, `PointConstraint(lon: float, lat: float, minutes: int = 15, mode: str = "foot-walking")`, `SearchRequest(query: str, point: PointConstraint | None = None)` — все pydantic `BaseModel`.
  - `llm.py`: `LLMResponse(content: str | None, tool_arguments: str | None)` (dataclass), `LLMClient` (Protocol) с методом `complete(messages: list[dict], tools: list[dict] | None = None, temperature: float = 0.0) -> LLMResponse`, `OpenRouterLLM(client=None)`, `FakeLLM(responses: list[LLMResponse])` с атрибутом `calls: list[dict]`, исключение `LLMUnavailable(RuntimeError)`.
  - `config.py`: поля `openrouter_api_key, llm_model, llm_fallbacks, llm_base_url, llm_timeout_s, reranker_model, ors_base_url, ors_api_key, rrf_k, retrieval_top_k, rerank_top_n, min_results, relaxation_max_iters, langfuse_enabled, langfuse_host, langfuse_public_key, langfuse_secret_key`.

- [ ] **Step 1: Написать падающие тесты схем**

`tests/test_online_schema.py`:

```python
import pytest
from pydantic import ValidationError
from habitus.online.schema import (GeoConstraint, ParsedQuery, PointConstraint,
                                   ResultItem, SearchRequest, SearchResponse)


def test_parsed_query_defaults():
    pq = ParsedQuery()
    assert pq.price_min is None and pq.price_max is None
    assert pq.geo == [] and pq.window_orientation == [] and pq.stop_factors == []
    assert pq.semantic_text == "" and pq.lang == "ru"


def test_parsed_query_full():
    pq = ParsedQuery(price_max=15_000_000, rooms=[1, 2],
                     geo=[GeoConstraint(kind="school", walk_minutes=10)],
                     noise_max="low", stop_factors=["bars"],
                     semantic_text="двор-колодец", lang="ru")
    assert pq.geo[0].kind == "school" and pq.rooms == [1, 2]


def test_parsed_query_rejects_bad_enum():
    with pytest.raises(ValidationError):
        ParsedQuery(noise_max="loud")
    with pytest.raises(ValidationError):
        GeoConstraint(kind="shop", walk_minutes=5)


def test_search_response_roundtrip():
    resp = SearchResponse(
        results=[ResultItem(external_id="E1", price=10_000_000, area=45.0,
                            rooms=2, address_facts={"noise_level": "low"}, score=0.9)],
        explanation="тихо и школа рядом", parsed=ParsedQuery(),
        data_freshness="данные актуальны на 2026-07-11 10:00")
    again = SearchResponse.model_validate(resp.model_dump())
    assert again.results[0].external_id == "E1"
    assert again.relaxed == [] and again.degraded == []


def test_search_request_requires_query():
    with pytest.raises(ValidationError):
        SearchRequest(query="")
    req = SearchRequest(query="тихо", point=PointConstraint(lon=37.6, lat=55.7))
    assert req.point.minutes == 15 and req.point.mode == "foot-walking"
```

- [ ] **Step 2: Запустить — убедиться, что падает**

Run: `uv run pytest tests/test_online_schema.py -v`
Expected: FAIL (`ModuleNotFoundError: No module named 'habitus.online'`)

- [ ] **Step 3: Создать пакет и схемы**

`habitus/online/__init__.py` — пустой файл.

`habitus/online/schema.py` (ParsedQuery/ResultItem/SearchResponse — точно по разделу 2 спеки):

```python
# habitus/online/schema.py — единственный источник правды по формам данных online-фазы
from typing import Literal
from pydantic import BaseModel, Field


class GeoConstraint(BaseModel):
    kind: Literal["school", "metro", "park"]
    walk_minutes: int          # порог пешей доступности


class ParsedQuery(BaseModel):
    price_min: int | None = None
    price_max: int | None = None
    rooms: list[int] | None = None            # [1,2] = «1-2 комнаты»
    area_min: float | None = None
    area_max: float | None = None
    geo: list[GeoConstraint] = []
    window_orientation: list[str] = []        # ["SW","W"]
    noise_max: Literal["low", "medium", "high"] | None = None
    stop_factors: list[str] = []              # ["bars","communal_flats"]
    semantic_text: str = ""                   # остаток для dense/sparse («двор-колодец»)
    lang: Literal["ru", "en"] = "ru"


class ResultItem(BaseModel):
    external_id: str
    price: int | None
    area: float | None
    rooms: int | None
    address_facts: dict          # walk_min_*, bar_density_500m, noise_level, orientation
    score: float                 # финальный score после реранка


class SearchResponse(BaseModel):
    results: list[ResultItem]
    explanation: str             # только поверх фактов из БД
    parsed: ParsedQuery          # что поняли (прозрачность/дебаг)
    relaxed: list[str] = []      # какие ограничения ослаблены relaxation-петлёй
    data_freshness: str          # «данные актуальны на …» (max updated_at)
    degraded: list[str] = []     # какие слои отвалились: "nlu"/"vector"/"reranker"/"llm"


class PointConstraint(BaseModel):
    """Кастомная гео-точка (компромисс «Сколково↔Сити»)."""
    lon: float
    lat: float
    minutes: int = 15
    mode: str = "foot-walking"


class SearchRequest(BaseModel):
    query: str = Field(min_length=1)
    point: PointConstraint | None = None
```

- [ ] **Step 4: Запустить тесты схем — PASS**

Run: `uv run pytest tests/test_online_schema.py -v`
Expected: PASS (5 тестов)

- [ ] **Step 5: Написать падающие тесты LLM-клиента**

`tests/test_llm.py`:

```python
import pytest
from habitus.config import settings
from habitus.online.llm import FakeLLM, LLMResponse, LLMUnavailable, OpenRouterLLM


def test_config_has_online_fields():
    assert settings.llm_base_url == "https://openrouter.ai/api/v1"
    assert settings.reranker_model == "BAAI/bge-reranker-v2-m3"
    assert settings.rrf_k == 60 and settings.retrieval_top_k == 50
    assert settings.rerank_top_n == 10 and settings.min_results == 5
    assert settings.relaxation_max_iters == 3
    assert isinstance(settings.llm_fallbacks, list) and settings.llm_fallbacks


def test_fake_llm_scripted_and_records_calls():
    fake = FakeLLM([LLMResponse(content="ответ", tool_arguments=None)])
    resp = fake.complete([{"role": "user", "content": "привет"}])
    assert resp.content == "ответ"
    assert fake.calls[0]["messages"][0]["content"] == "привет"
    assert fake.calls[0]["temperature"] == 0.0


def test_fake_llm_exhausted_raises():
    fake = FakeLLM([])
    with pytest.raises(LLMUnavailable):
        fake.complete([{"role": "user", "content": "x"}])


class _FakeMsg:
    def __init__(self, content=None, tool_calls=None):
        self.content, self.tool_calls = content, tool_calls


class _FakeCompletion:
    def __init__(self, msg):
        self.choices = [type("C", (), {"message": msg})()]


class _FakeOpenAI:
    """Первая модель падает, вторая отвечает — проверяем фолбэк-цепочку."""
    def __init__(self):
        self.models_tried = []
        chat = type("Chat", (), {})()
        chat.completions = self
        self.chat = chat

    def create(self, *, model, messages, temperature, **kw):
        self.models_tried.append(model)
        if len(self.models_tried) == 1:
            raise TimeoutError("primary down")
        return _FakeCompletion(_FakeMsg(content="ok"))


def test_openrouter_fallback_chain():
    fake_client = _FakeOpenAI()
    llm = OpenRouterLLM(client=fake_client)
    resp = llm.complete([{"role": "user", "content": "q"}])
    assert resp.content == "ok"
    assert fake_client.models_tried[0] == settings.llm_model
    assert fake_client.models_tried[1] == settings.llm_fallbacks[0]


def test_openrouter_all_models_down():
    class _AllDown(_FakeOpenAI):
        def create(self, **kw):
            self.models_tried.append(kw["model"])
            raise TimeoutError("down")
    llm = OpenRouterLLM(client=_AllDown())
    with pytest.raises(LLMUnavailable):
        llm.complete([{"role": "user", "content": "q"}])
```

- [ ] **Step 6: Запустить — убедиться, что падает**

Run: `uv run pytest tests/test_llm.py -v`
Expected: FAIL (`ModuleNotFoundError`/`ImportError` по `habitus.online.llm`, затем AssertionError по полям config)

- [ ] **Step 7: Расширить config и добавить зависимость openai**

Run: `uv add "openai>=1.40"`

`habitus/config.py` — добавить поля в класс `Settings` (после `kaggle_key`):

```python
    # --- online-фаза ---
    openrouter_api_key: str = ""
    llm_model: str = "qwen/qwen-2.5-72b-instruct"
    llm_fallbacks: list[str] = ["deepseek/deepseek-chat", "openai/gpt-4o-mini"]
    llm_base_url: str = "https://openrouter.ai/api/v1"
    llm_timeout_s: float = 30.0
    reranker_model: str = "BAAI/bge-reranker-v2-m3"
    ors_base_url: str = "https://api.openrouteservice.org"
    ors_api_key: str = ""
    rrf_k: int = 60
    retrieval_top_k: int = 50
    rerank_top_n: int = 10
    min_results: int = 5              # порог relaxation-петли
    relaxation_max_iters: int = 3
    langfuse_enabled: bool = False
    langfuse_host: str = ""
    langfuse_public_key: str = ""
    langfuse_secret_key: str = ""
```

- [ ] **Step 8: Реализовать `habitus/online/llm.py`**

```python
# habitus/online/llm.py — LLM-доступ: Protocol + OpenRouter (Qwen, фолбэки) + Fake
from dataclasses import dataclass
from typing import Protocol
from habitus.config import settings


class LLMUnavailable(RuntimeError):
    """Все модели цепочки недоступны / ответы Fake исчерпаны."""


@dataclass
class LLMResponse:
    content: str | None            # обычный текстовый ответ
    tool_arguments: str | None     # сырой JSON аргументов tool-call (если был)


class LLMClient(Protocol):
    def complete(self, messages: list[dict], tools: list[dict] | None = None,
                 temperature: float = 0.0) -> LLMResponse: ...


class OpenRouterLLM:
    """openai-SDK поверх OpenRouter. Primary settings.llm_model,
    при любой ошибке — фолбэк-цепочка settings.llm_fallbacks."""

    def __init__(self, client=None):
        if client is None:
            from openai import OpenAI
            client = OpenAI(base_url=settings.llm_base_url,
                            api_key=settings.openrouter_api_key,
                            timeout=settings.llm_timeout_s)
        self._client = client

    def complete(self, messages: list[dict], tools: list[dict] | None = None,
                 temperature: float = 0.0) -> LLMResponse:
        last_err: Exception | None = None
        for model in [settings.llm_model, *settings.llm_fallbacks]:
            kwargs = {"model": model, "messages": messages, "temperature": temperature}
            if tools:
                kwargs["tools"] = tools
                kwargs["tool_choice"] = {"type": "function",
                                         "function": {"name": tools[0]["function"]["name"]}}
            try:
                r = self._client.chat.completions.create(**kwargs)
            except Exception as e:          # таймаут/5xx/лимиты → следующая модель
                last_err = e
                continue
            msg = r.choices[0].message
            args = None
            if getattr(msg, "tool_calls", None):
                args = msg.tool_calls[0].function.arguments
            return LLMResponse(content=msg.content, tool_arguments=args)
        raise LLMUnavailable(f"все модели цепочки недоступны: {last_err}")


class FakeLLM:
    """Скриптованные ответы для детерминированных тестов (без сети)."""

    def __init__(self, responses: list[LLMResponse]):
        self.responses = list(responses)
        self.calls: list[dict] = []

    def complete(self, messages: list[dict], tools: list[dict] | None = None,
                 temperature: float = 0.0) -> LLMResponse:
        self.calls.append({"messages": list(messages), "tools": tools,
                           "temperature": temperature})
        if not self.responses:
            raise LLMUnavailable("FakeLLM: скриптованные ответы исчерпаны")
        return self.responses.pop(0)
```

- [ ] **Step 9: Запустить все тесты задачи — PASS**

Run: `uv run pytest tests/test_online_schema.py tests/test_llm.py -v`
Expected: PASS (10 тестов)

- [ ] **Step 10: Commit**

```bash
git add habitus/config.py habitus/online/ tests/test_online_schema.py tests/test_llm.py pyproject.toml uv.lock
git commit -m "feat: фундамент online-фазы — схемы контракта, конфиг, LLM-клиент с фолбэками"
```

---

### Task 2: Гибридный retrieval — RRF, WHERE-билдер, dense+sparse, HNSW-guard

**Files:**
- Create: `habitus/online/retrieval.py`
- Test: `tests/test_retrieval.py` (юниты без БД)
- Test: `tests/test_retrieval_db.py` (интеграция на dev-Postgres 5544)

**Interfaces:**
- Consumes: `ParsedQuery`, `GeoConstraint` из `habitus.online.schema` (Task 1); `encode_texts(texts, model=None)`, `to_sparsevec_literal(sparse, dim)`, `SPARSE_DIM` из `habitus.embed.encode`; `settings.rrf_k`, `settings.retrieval_top_k`.
- Produces (на них опираются Tasks 4, 6, 8, 9):
  - `@dataclass Candidate: external_id: str; doc_text: str; price: int | None; area: float | None; rooms: int | None; facts: dict; score: float; updated_at: datetime`
  - `rrf_merge(rankings: Sequence[Sequence[str]], k: int = 60) -> list[tuple[str, float]]`
  - `build_where(pq: ParsedQuery, extra_sql: str | None = None, extra_params: Sequence = ()) -> tuple[str, list]`
  - `encode_query(text: str, model=None) -> tuple[list[float], dict[int, float]]`
  - `hybrid_search(conn, pq: ParsedQuery, *, model=None, top_k: int | None = None, geo_sql: str | None = None, geo_params: Sequence = (), query_vec: tuple[list[float], dict[int, float]] | None = None, channels: tuple[str, ...] = ("dense", "sparse")) -> list[Candidate]`
  - `filter_only_search(conn, pq, top_k: int | None = None, geo_sql: str | None = None, geo_params: Sequence = ()) -> list[Candidate]`

- [ ] **Step 1: Юнит-тесты RRF и WHERE-билдера (падающие)**

`tests/test_retrieval.py`:

```python
import pytest
from habitus.online.retrieval import build_where, rrf_merge
from habitus.online.schema import GeoConstraint, ParsedQuery


def test_rrf_merge_two_lists():
    merged = rrf_merge([["a", "b", "c"], ["b", "a"]], k=60)
    scores = dict(merged)
    assert scores["a"] == pytest.approx(1 / 61 + 1 / 62)
    assert scores["b"] == pytest.approx(1 / 62 + 1 / 61)
    assert scores["c"] == pytest.approx(1 / 63)
    assert merged[-1][0] == "c"                      # худший — только в одном списке


def test_rrf_merge_single_list_keeps_order():
    merged = rrf_merge([["x", "y"]], k=60)
    assert [eid for eid, _ in merged] == ["x", "y"]


def test_rrf_merge_tie_breaks_by_id():
    # одинаковые score → детерминированный порядок по external_id
    merged = rrf_merge([["b"], ["a"]], k=60)
    assert [eid for eid, _ in merged] == ["a", "b"]


def test_build_where_empty_query_only_active():
    sql, params = build_where(ParsedQuery())
    assert sql == "is_active = TRUE" and params == []


def test_build_where_full():
    pq = ParsedQuery(price_min=1, price_max=2, rooms=[1, 2], area_min=30.0,
                     area_max=60.0,
                     geo=[GeoConstraint(kind="school", walk_minutes=10),
                          GeoConstraint(kind="metro", walk_minutes=7)],
                     window_orientation=["SW", "W"], noise_max="medium",
                     stop_factors=["bars"], semantic_text="x")
    sql, params = build_where(pq)
    assert "price >= %s" in sql and "price <= %s" in sql
    assert "rooms = ANY(%s)" in sql
    assert "area >= %s" in sql and "area <= %s" in sql
    assert "walk_min_school <= %s" in sql and "walk_min_metro <= %s" in sql
    assert "noise_level = ANY(%s)" in sql
    assert "window_orientation && %s" in sql
    assert "bar_density_500m = 0" in sql
    # порядок параметров = порядку клауз
    assert params == [1, 2, [1, 2], 30.0, 60.0, 10, 7, ["low", "medium"], ["SW", "W"]]


def test_build_where_noise_high_means_no_filter():
    sql, _ = build_where(ParsedQuery(noise_max="high"))
    assert "noise_level" not in sql


def test_build_where_unknown_stop_factor_ignored():
    sql, _ = build_where(ParsedQuery(stop_factors=["communal_flats"]))
    assert "bar_density" not in sql            # колонки под это нет — молча пропускаем


def test_build_where_extra_geo_predicate():
    sql, params = build_where(ParsedQuery(), extra_sql="ST_DWithin(geom, %s, %s)",
                              extra_params=("PT", 500))
    assert sql.endswith("AND ST_DWithin(geom, %s, %s)")
    assert params == ["PT", 500]
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_retrieval.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.retrieval`)

- [ ] **Step 3: Реализовать чистые функции + поисковые каналы**

`habitus/online/retrieval.py` (целиком):

```python
# habitus/online/retrieval.py — сердце RAG: WHERE-фильтр + dense + sparse + RRF
from dataclasses import dataclass
from datetime import datetime
from typing import Sequence

import psycopg
from psycopg.rows import dict_row

from habitus.config import settings
from habitus.embed.encode import SPARSE_DIM, encode_texts, to_sparsevec_literal
from habitus.online.schema import ParsedQuery

NOISE_ORDER = ["low", "medium", "high"]

# факты, которые едут в ResultItem.address_facts и в объяснение
FACT_COLUMNS = ("walk_min_school", "walk_min_metro", "walk_min_park",
                "bar_density_500m", "noise_level", "window_orientation")


@dataclass
class Candidate:
    external_id: str
    doc_text: str
    price: int | None
    area: float | None
    rooms: int | None
    facts: dict
    score: float
    updated_at: datetime


def rrf_merge(rankings: Sequence[Sequence[str]], k: int = 60) -> list[tuple[str, float]]:
    """Reciprocal Rank Fusion: score = Σ 1/(k+rank), rank с 1. Тай-брейк по id."""
    scores: dict[str, float] = {}
    for ranking in rankings:
        for rank, ext_id in enumerate(ranking, start=1):
            scores[ext_id] = scores.get(ext_id, 0.0) + 1.0 / (k + rank)
    return sorted(scores.items(), key=lambda kv: (-kv[1], kv[0]))


def build_where(pq: ParsedQuery, extra_sql: str | None = None,
                extra_params: Sequence = ()) -> tuple[str, list]:
    """ParsedQuery → параметризованный WHERE. Порядок клауз фиксирован."""
    clauses: list[str] = ["is_active = TRUE"]
    params: list = []
    if pq.price_min is not None:
        clauses.append("price >= %s"); params.append(pq.price_min)
    if pq.price_max is not None:
        clauses.append("price <= %s"); params.append(pq.price_max)
    if pq.rooms:
        clauses.append("rooms = ANY(%s)"); params.append(list(pq.rooms))
    if pq.area_min is not None:
        clauses.append("area >= %s"); params.append(pq.area_min)
    if pq.area_max is not None:
        clauses.append("area <= %s"); params.append(pq.area_max)
    for g in pq.geo:  # g.kind — Literal["school","metro","park"] → имя колонки безопасно
        clauses.append(f"walk_min_{g.kind} <= %s"); params.append(g.walk_minutes)
    if pq.noise_max is not None and pq.noise_max != "high":
        allowed = NOISE_ORDER[: NOISE_ORDER.index(pq.noise_max) + 1]
        clauses.append("noise_level = ANY(%s)"); params.append(allowed)
    if pq.window_orientation:
        clauses.append("window_orientation && %s"); params.append(list(pq.window_orientation))
    if "bars" in pq.stop_factors:
        clauses.append("bar_density_500m = 0")
    if extra_sql:
        clauses.append(extra_sql); params.extend(extra_params)
    return " AND ".join(clauses), params


def encode_query(text: str, model=None) -> tuple[list[float], dict[int, float]]:
    """Запрос → (dense 1024, sparse-веса) тем же BGE-M3, что и документы."""
    enc = encode_texts([text], model=model)[0]
    return enc["dense"], enc["sparse"]


def _vec_literal(dense: list[float]) -> str:
    return "[" + ",".join(f"{x:g}" for x in dense) + "]"


def _channel_search(conn: psycopg.Connection, sql: str, params: Sequence) -> list[str]:
    """Один векторный канал. Грабля фильтрованного HNSW: при жёстком WHERE индекс
    отдаёт < LIMIT строк — лечится iterative_scan=strict_order (pgvector >= 0.8).
    На старом pgvector GUC нет → savepoint откатывается, идём без него."""
    with conn.transaction():
        try:
            with conn.transaction():  # savepoint: ошибка SET не рушит внешний tx
                conn.execute("SET LOCAL hnsw.iterative_scan = 'strict_order';")
        except psycopg.errors.UndefinedObject:
            pass
        return [r[0] for r in conn.execute(sql, list(params)).fetchall()]


def _fetch_candidates(conn: psycopg.Connection, ext_ids: list[str],
                      scores: dict[str, float]) -> list[Candidate]:
    if not ext_ids:
        return []
    cols = ", ".join(("external_id", "doc_text", "price", "area", "rooms",
                      "updated_at") + FACT_COLUMNS)
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute(f"SELECT {cols} FROM listings WHERE external_id = ANY(%s);",
                    (ext_ids,))
        rows = {r["external_id"]: r for r in cur.fetchall()}
    out = []
    for eid in ext_ids:
        r = rows.get(eid)
        if r is None:
            continue
        out.append(Candidate(
            external_id=eid, doc_text=r["doc_text"] or "", price=r["price"],
            area=r["area"], rooms=r["rooms"],
            facts={c: r[c] for c in FACT_COLUMNS},
            score=scores.get(eid, 0.0), updated_at=r["updated_at"]))
    return out


def filter_only_search(conn: psycopg.Connection, pq: ParsedQuery,
                       top_k: int | None = None, geo_sql: str | None = None,
                       geo_params: Sequence = ()) -> list[Candidate]:
    """Деградация «без вектора»: только SQL-фильтры, свежие сверху."""
    k = top_k or settings.retrieval_top_k
    where, params = build_where(pq, geo_sql, geo_params)
    with conn.cursor() as cur:
        cur.execute(f"SELECT external_id FROM listings WHERE {where} "
                    f"ORDER BY updated_at DESC LIMIT %s;", params + [k])
        ids = [r[0] for r in cur.fetchall()]
    return _fetch_candidates(conn, ids, {})


def hybrid_search(conn: psycopg.Connection, pq: ParsedQuery, *, model=None,
                  top_k: int | None = None, geo_sql: str | None = None,
                  geo_params: Sequence = (),
                  query_vec: tuple[list[float], dict[int, float]] | None = None,
                  channels: tuple[str, ...] = ("dense", "sparse")) -> list[Candidate]:
    """WHERE + dense + sparse → RRF → top-K кандидатов (порядок RRF)."""
    k = top_k or settings.retrieval_top_k
    if query_vec is None:
        if not pq.semantic_text:
            return filter_only_search(conn, pq, k, geo_sql, geo_params)
        query_vec = encode_query(pq.semantic_text, model=model)
    qdense, qsparse = query_vec

    where, params = build_where(pq, geo_sql, geo_params)
    rankings: list[list[str]] = []
    if "dense" in channels:
        rankings.append(_channel_search(
            conn,
            f"SELECT external_id FROM listings WHERE {where} "
            f"AND embedding IS NOT NULL ORDER BY embedding <=> %s::vector LIMIT %s;",
            params + [_vec_literal(qdense), k]))
    if "sparse" in channels:
        rankings.append(_channel_search(
            conn,
            f"SELECT external_id FROM listings WHERE {where} "
            f"AND sparse_embedding IS NOT NULL "
            f"ORDER BY sparse_embedding <=> %s::sparsevec LIMIT %s;",
            params + [to_sparsevec_literal(qsparse, SPARSE_DIM), k]))

    merged = rrf_merge(rankings, k=settings.rrf_k)[:k]
    ids = [eid for eid, _ in merged]
    return _fetch_candidates(conn, ids, dict(merged))
```

- [ ] **Step 4: Запустить юниты — PASS**

Run: `uv run pytest tests/test_retrieval.py -v`
Expected: PASS (8 тестов)

- [ ] **Step 5: Commit чистых функций**

```bash
git add habitus/online/retrieval.py tests/test_retrieval.py
git commit -m "feat: гибридный retrieval — RRF-слияние и WHERE-билдер из ParsedQuery"
```

- [ ] **Step 6: Интеграционные тесты на dev-Postgres (падающие проверки поведения)**

`tests/test_retrieval_db.py`. Запрос кодируем рукописным `query_vec` — реальная BGE-M3 не нужна. Оси dense: A→ось 0, B→ось 1, C→ось 2.

```python
import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import SPARSE_DIM, to_sparsevec_literal
from habitus.online.retrieval import filter_only_search, hybrid_search
from habitus.online.schema import GeoConstraint, ParsedQuery

DIM = 1024


def _axis(i: int) -> list[float]:
    v = [0.0] * DIM
    v[i] = 1.0
    return v


def _vec(v: list[float]) -> str:
    return "[" + ",".join(f"{x:g}" for x in v) + "]"


ROWS = [
    # (eid, price, rooms, walk_school, bars, noise, dense_axis, sparse)
    ("A", 10_000_000, 2, 8.0, 0, "low", 0, {10: 1.0}),
    ("B", 12_000_000, 2, 9.0, 0, "low", 1, {20: 1.0}),
    ("C", 30_000_000, 3, 25.0, 3, "high", 2, {30: 1.0}),
]


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for eid, price, rooms, ws, bars, noise, axis, sparse in ROWS:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, price,
                           rooms, area, walk_min_school, bar_density_500m, noise_level,
                           window_orientation, doc_text, embedding, sparse_embedding)
                       VALUES (%s,'test',TRUE,%s,%s,50,%s,%s,%s,%s,%s,
                               %s::vector,%s::sparsevec);""",
                    (eid, price, rooms, ws, bars, noise, ["SW"],
                     f"объект {eid}", _vec(_axis(axis)),
                     to_sparsevec_literal(sparse, SPARSE_DIM)))
        c.commit()
        yield c


def test_rrf_fuses_dense_and_sparse(conn):
    # dense ближе всех к A (ось 0), sparse матчит B (токен 20)
    cands = hybrid_search(conn, ParsedQuery(semantic_text="x"),
                          query_vec=(_axis(0), {20: 1.0}))
    top2 = {c.external_id for c in cands[:2]}
    assert top2 == {"A", "B"}
    assert all(c.score > 0 for c in cands)
    assert cands[0].facts["noise_level"] in ("low", "high")   # факты доехали


def test_hard_filters_exclude(conn):
    pq = ParsedQuery(price_max=15_000_000, noise_max="low",
                     stop_factors=["bars"],
                     geo=[GeoConstraint(kind="school", walk_minutes=10)],
                     semantic_text="x")
    cands = hybrid_search(conn, pq, query_vec=(_axis(2), {30: 1.0}))
    ids = [c.external_id for c in cands]
    assert "C" not in ids and set(ids) == {"A", "B"}


def test_filtered_hnsw_returns_all_matches(conn):
    # грабля: жёсткий WHERE + HNSW без strict_order отдаёт < LIMIT.
    # Оба подходящих объекта обязаны вернуться.
    cands = hybrid_search(conn, ParsedQuery(price_max=15_000_000, semantic_text="x"),
                          query_vec=(_axis(2), {}), channels=("dense",))
    assert {c.external_id for c in cands} == {"A", "B"}


def test_dense_only_channel(conn):
    cands = hybrid_search(conn, ParsedQuery(semantic_text="x"),
                          query_vec=(_axis(1), {}), channels=("dense",))
    assert cands[0].external_id == "B"


def test_filter_only_search(conn):
    cands = filter_only_search(conn, ParsedQuery(rooms=[2]))
    assert {c.external_id for c in cands} == {"A", "B"}


def test_empty_semantic_text_falls_back_to_filters(conn):
    cands = hybrid_search(conn, ParsedQuery(rooms=[3]))
    assert [c.external_id for c in cands] == ["C"]
```

- [ ] **Step 7: Запустить интеграцию — PASS (реализация уже есть; чинить, если поведение не совпало)**

Run: `uv run pytest tests/test_retrieval_db.py -v`
Expected: PASS (6 тестов). Если `SET LOCAL hnsw.iterative_scan` падает не `UndefinedObject` — проверить версию pgvector в dev-БД (`SELECT extversion FROM pg_extension WHERE extname='vector';`).

- [ ] **Step 8: Commit**

```bash
git add tests/test_retrieval_db.py habitus/online/retrieval.py
git commit -m "feat: dense+sparse каналы с guard фильтрованного HNSW, интеграционные тесты retrieval"
```

---

### Task 3: Linguistic Agent (NLU) — промпт, tool-схема, retry-петля

**Files:**
- Create: `habitus/online/nlu.py`
- Test: `tests/test_nlu.py`

**Interfaces:**
- Consumes: `ParsedQuery` из `habitus.online.schema`; `LLMClient`, `LLMResponse`, `FakeLLM`, `LLMUnavailable` из `habitus.online.llm` (Task 1).
- Produces (на них опираются Tasks 8, 9):
  - `SYSTEM_PROMPT: str`, `PARSE_TOOL: dict`
  - `class ParseError(RuntimeError)`
  - `parse_query(text: str, llm: LLMClient, max_retries: int = 3) -> ParsedQuery`

- [ ] **Step 1: Падающие тесты retry-петли на FakeLLM**

`tests/test_nlu.py`:

```python
import json
import pytest
from habitus.online.llm import FakeLLM, LLMResponse
from habitus.online.nlu import PARSE_TOOL, ParseError, parse_query


def _tool_resp(payload: dict) -> LLMResponse:
    return LLMResponse(content=None,
                       tool_arguments=json.dumps(payload, ensure_ascii=False))


def test_parse_query_first_try():
    fake = FakeLLM([_tool_resp({"price_max": 15_000_000, "rooms": [2],
                                "noise_max": "low", "stop_factors": ["bars"],
                                "semantic_text": "тихо"})])
    pq = parse_query("тихая двушка до 15 млн без баров", fake)
    assert pq.price_max == 15_000_000 and pq.rooms == [2]
    assert pq.noise_max == "low" and pq.stop_factors == ["bars"]
    # LLM вызван с tool-схемой ParsedQuery и temperature=0
    call = fake.calls[0]
    assert call["temperature"] == 0.0
    assert call["tools"][0]["function"]["name"] == "submit_parsed_query"
    assert "price_max" in json.dumps(call["tools"][0]["function"]["parameters"])


def test_parse_query_retry_feeds_error_back_to_model():
    fake = FakeLLM([
        LLMResponse(content="это не json", tool_arguments=None),      # 1-я попытка
        _tool_resp({"rooms": [1, 2], "semantic_text": ""}),           # самопочинка
    ])
    pq = parse_query("1-2 комнаты", fake)
    assert pq.rooms == [1, 2]
    # во 2-м вызове модели вернули текст ошибки валидации
    retry_messages = fake.calls[1]["messages"]
    assert retry_messages[-2]["role"] == "assistant"
    assert "не прошёл валидацию" in retry_messages[-1]["content"]


def test_parse_query_invalid_schema_then_fixed():
    fake = FakeLLM([
        _tool_resp({"noise_max": "loud"}),                            # мимо enum
        _tool_resp({"noise_max": "low"}),
    ])
    pq = parse_query("тихо", fake)
    assert pq.noise_max == "low"


def test_parse_query_exhausted_raises():
    fake = FakeLLM([LLMResponse(content="мусор", tool_arguments=None)] * 3)
    with pytest.raises(ParseError):
        parse_query("запрос", fake, max_retries=3)


def test_system_prompt_covers_cross_language():
    from habitus.online.nlu import SYSTEM_PROMPT
    assert "английск" in SYSTEM_PROMPT.lower()   # few-shot кросс-языка присутствует
    assert "semantic_text" in SYSTEM_PROMPT
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_nlu.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.nlu`)

- [ ] **Step 3: Реализовать `habitus/online/nlu.py`**

```python
# habitus/online/nlu.py — Linguistic Agent: свободный текст → ParsedQuery
from pydantic import ValidationError
from habitus.online.llm import LLMClient
from habitus.online.schema import ParsedQuery


class ParseError(RuntimeError):
    """NLU не смог получить валидный ParsedQuery за max_retries попыток."""


SYSTEM_PROMPT = """Ты — парсер запросов по недвижимости Москвы. Извлеки из запроса \
пользователя ТОЛЬКО явно указанные ограничения и вызови инструмент submit_parsed_query.

Правила:
- Не выдумывай значения: поле заполняется, только если оно явно есть в запросе.
- Жёсткие числовые/категориальные условия → поля фильтров; атмосфера и образы \
(«двор-колодец», «сталинка», «видовая») → semantic_text.
- «бюджет бизнес» ≈ price_max 40000000; «эконом» ≈ price_max 15000000 (Москва, рубли).
- Стороны света: юго-запад → ["SW"], запад → ["W"], юг → ["S"] и т.п.
- «тихо», «не шумно» → noise_max="low". «без баров» → stop_factors=["bars"].
- «рядом/near» без числа минут → walk_minutes 15.
- Запрос на английском языке → те же поля; semantic_text оставь на языке запроса, \
lang="en".

Примеры:
Запрос: «двушка или трёшка до 20 млн, школа в 10 минутах пешком, окна на юго-запад»
→ {"price_max": 20000000, "rooms": [2, 3], "geo": [{"kind": "school", \
"walk_minutes": 10}], "window_orientation": ["SW"], "semantic_text": "", "lang": "ru"}

Запрос: «работаем в Сколково и в Сити, нужен компромисс, тихий двор без баров»
→ {"noise_max": "low", "stop_factors": ["bars"], \
"semantic_text": "компромисс между Сколково и Сити, тихий двор", "lang": "ru"}

Запрос: "quiet flat near a strong school, no bars around"
→ {"geo": [{"kind": "school", "walk_minutes": 15}], "noise_max": "low", \
"stop_factors": ["bars"], "semantic_text": "quiet flat near a strong school", \
"lang": "en"}
"""

PARSE_TOOL = {
    "type": "function",
    "function": {
        "name": "submit_parsed_query",
        "description": "Структурированный разбор запроса по недвижимости",
        "parameters": ParsedQuery.model_json_schema(),
    },
}


def parse_query(text: str, llm: LLMClient, max_retries: int = 3) -> ParsedQuery:
    """Вызов LLM с tool-схемой; невалидный ответ → текст ошибки обратно модели."""
    messages = [{"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": text}]
    last_err = ""
    for _ in range(max_retries):
        resp = llm.complete(messages, tools=[PARSE_TOOL], temperature=0.0)
        raw = resp.tool_arguments or resp.content or ""
        try:
            return ParsedQuery.model_validate_json(raw)
        except ValidationError as e:
            last_err = str(e)
            messages.append({"role": "assistant", "content": raw})
            messages.append({"role": "user", "content":
                             f"Ответ не прошёл валидацию схемы: {last_err}\n"
                             f"Верни исправленный JSON строго по схеме "
                             f"submit_parsed_query."})
    raise ParseError(f"NLU: нет валидного ParsedQuery за {max_retries} попыток: "
                     f"{last_err}")
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_nlu.py -v`
Expected: PASS (5 тестов)

- [ ] **Step 5: Commit**

```bash
git add habitus/online/nlu.py tests/test_nlu.py
git commit -m "feat: Linguistic Agent — tool-схема ParsedQuery, few-shot промпт, retry-петля самопочинки"
```

---

### Task 4: Reranker — bge-reranker-v2-m3, ленивая загрузка, top-N

**Files:**
- Create: `habitus/online/rerank.py`
- Test: `tests/test_rerank.py`

**Interfaces:**
- Consumes: `Candidate` из `habitus.online.retrieval` (Task 2); `settings.reranker_model`, `settings.rerank_top_n`.
- Produces (на них опираются Tasks 8, 9):
  - `get_reranker()` — ленивый синглтон `FlagReranker`
  - `rerank(query: str, candidates: list[Candidate], top_n: int | None = None, reranker=None) -> list[Candidate]` — отсортировано по убыванию скора реранкера, `score` кандидатов перезаписан, срез top-N.

- [ ] **Step 1: Падающие тесты на фейковом реранкере**

`tests/test_rerank.py`:

```python
from datetime import datetime, timezone
from habitus.online.rerank import rerank
from habitus.online.retrieval import Candidate


def _cand(eid: str, doc: str) -> Candidate:
    return Candidate(external_id=eid, doc_text=doc, price=None, area=None,
                     rooms=None, facts={}, score=0.0,
                     updated_at=datetime(2026, 7, 1, tzinfo=timezone.utc))


class FakeReranker:
    """Скорит по вхождению слова «школа» — детерминированно, без модели."""
    def __init__(self):
        self.pairs = None

    def compute_score(self, pairs, normalize=True):
        self.pairs = pairs
        return [0.9 if "школа" in doc else 0.1 for _, doc in pairs]


def test_rerank_orders_and_cuts_top_n():
    cands = [_cand("A", "просто квартира"), _cand("B", "школа рядом"),
             _cand("C", "ещё вариант")]
    fr = FakeReranker()
    out = rerank("школа в 10 минутах", cands, top_n=2, reranker=fr)
    assert [c.external_id for c in out] == ["B", "A"]   # tie A/C — стабильный порядок
    assert out[0].score == 0.9 and out[1].score == 0.1
    # пары (запрос, doc_text) ушли в реранкер
    assert fr.pairs[0] == ["школа в 10 минутах", "просто квартира"]


def test_rerank_single_candidate_scalar_score():
    class ScalarReranker:
        def compute_score(self, pairs, normalize=True):
            return 0.42          # FlagReranker для одной пары возвращает скаляр
    out = rerank("q", [_cand("A", "doc")], reranker=ScalarReranker())
    assert len(out) == 1 and out[0].score == 0.42


def test_rerank_empty_input():
    assert rerank("q", [], reranker=None) == []
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_rerank.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.rerank`)

- [ ] **Step 3: Реализовать `habitus/online/rerank.py`**

```python
# habitus/online/rerank.py — bge-reranker-v2-m3, ленивая загрузка (как get_model в embed)
from dataclasses import replace
from habitus.config import settings
from habitus.online.retrieval import Candidate

_reranker = None


def get_reranker():
    global _reranker
    if _reranker is None:
        from FlagEmbedding import FlagReranker
        _reranker = FlagReranker(settings.reranker_model, use_fp16=True)
    return _reranker


def rerank(query: str, candidates: list[Candidate], top_n: int | None = None,
           reranker=None) -> list[Candidate]:
    """(запрос, doc_text) пары → скоры реранкера → top-N по убыванию."""
    if not candidates:
        return []
    n = top_n or settings.rerank_top_n
    r = reranker or get_reranker()
    scores = r.compute_score([[query, c.doc_text] for c in candidates],
                             normalize=True)
    if not isinstance(scores, list):        # одна пара → скаляр
        scores = [scores]
    ranked = sorted(zip(candidates, scores), key=lambda p: -p[1])
    return [replace(c, score=float(s)) for c, s in ranked[:n]]
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_rerank.py -v`
Expected: PASS (3 теста)

- [ ] **Step 5: Commit**

```bash
git add habitus/online/rerank.py tests/test_rerank.py
git commit -m "feat: reranker bge-reranker-v2-m3 с ленивой загрузкой и top-N"
```

---

### Task 5: Geo-агент — IsochroneProvider, ORS, центральная точка, SQL-предикат

**Files:**
- Create: `habitus/online/geo.py`
- Test: `tests/test_geo_agent.py`

**Interfaces:**
- Consumes: `settings.ors_base_url`, `settings.ors_api_key`; `requests` (уже в зависимостях).
- Produces (на них опираются Tasks 6, 8):
  - `WALK_SPEED_M_PER_MIN: float = 80.0`
  - `IsochroneProvider` (Protocol): `isochrone(lon: float, lat: float, minutes: int, mode: str = "foot-walking") -> dict` (GeoJSON-геометрия)
  - `ORSProvider(session=None)` — реальный клиент OpenRouteService
  - `midpoint(a: tuple[float, float], b: tuple[float, float]) -> tuple[float, float]` — центральная точка компромисса «двух работ»
  - `point_predicate(lon: float, lat: float, minutes: int, provider: IsochroneProvider | None = None) -> tuple[str, tuple]` — SQL-предикат для `build_where(extra_sql=...)`. `provider=None` — Precomputed-путь без сети: круг `ST_DWithin` радиусом `minutes * 80 м`; с провайдером — `ST_Within` по изохрон-полигону.

Примечание к спеке: гео-ограничения из `ParsedQuery.geo` (школа/метро/парк) решаются готовыми колонками `walk_min_*` уже в `build_where` (Task 2) — это и есть Precomputed-дефолт; `point_predicate` нужен только для кастомной точки.

- [ ] **Step 1: Падающие тесты**

`tests/test_geo_agent.py`:

```python
import json
from habitus.config import settings
from habitus.online.geo import ORSProvider, midpoint, point_predicate


def test_midpoint():
    assert midpoint((37.0, 55.0), (38.0, 56.0)) == (37.5, 55.5)


def test_point_predicate_default_is_dwithin_circle():
    sql, params = point_predicate(37.6, 55.7, 15)
    assert "ST_DWithin" in sql and "geography" in sql
    assert params == (37.6, 55.7, 1200.0)      # 15 мин * 80 м/мин


class FakeIsochrone:
    def isochrone(self, lon, lat, minutes, mode="foot-walking"):
        return {"type": "Polygon",
                "coordinates": [[[37, 55], [38, 55], [38, 56], [37, 55]]]}


def test_point_predicate_with_provider_uses_polygon():
    sql, params = point_predicate(37.6, 55.7, 15, provider=FakeIsochrone())
    assert "ST_Within" in sql and "ST_GeomFromGeoJSON" in sql
    assert json.loads(params[0])["type"] == "Polygon"


class _FakeResp:
    status_code = 200
    def raise_for_status(self): pass
    def json(self):
        return {"features": [{"geometry": {"type": "Polygon",
                                           "coordinates": [[[1, 2]]]}}]}


class FakeSession:
    def __init__(self):
        self.url = self.payload = self.headers = None

    def post(self, url, json=None, headers=None, timeout=None):
        self.url, self.payload, self.headers = url, json, headers
        return _FakeResp()


def test_ors_provider_builds_request():
    s = FakeSession()
    poly = ORSProvider(session=s).isochrone(37.6, 55.7, 10)
    assert poly["type"] == "Polygon"
    assert s.url == f"{settings.ors_base_url}/v2/isochrones/foot-walking"
    assert s.payload == {"locations": [[37.6, 55.7]], "range": [600],
                         "range_type": "time"}
    assert s.headers["Authorization"] == settings.ors_api_key
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_geo_agent.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.geo`)

- [ ] **Step 3: Реализовать `habitus/online/geo.py`**

```python
# habitus/online/geo.py — Geo-Spatial Agent: изохроны и SQL-гео-предикаты
import json
from typing import Protocol

import requests
from habitus.config import settings

WALK_SPEED_M_PER_MIN = 80.0        # пешеход ~4.8 км/ч


class IsochroneProvider(Protocol):
    def isochrone(self, lon: float, lat: float, minutes: int,
                  mode: str = "foot-walking") -> dict: ...


class ORSProvider:
    """Реальный клиент OpenRouteService/Valhalla-совместимого API."""

    def __init__(self, session=None):
        self._session = session or requests.Session()

    def isochrone(self, lon: float, lat: float, minutes: int,
                  mode: str = "foot-walking") -> dict:
        resp = self._session.post(
            f"{settings.ors_base_url}/v2/isochrones/{mode}",
            json={"locations": [[lon, lat]], "range": [minutes * 60],
                  "range_type": "time"},
            headers={"Authorization": settings.ors_api_key},
            timeout=15)
        resp.raise_for_status()
        return resp.json()["features"][0]["geometry"]


def midpoint(a: tuple[float, float], b: tuple[float, float]) -> tuple[float, float]:
    """Центральная точка компромисса («работа в Сколково ↔ офис в Сити»)."""
    return ((a[0] + b[0]) / 2, (a[1] + b[1]) / 2)


def point_predicate(lon: float, lat: float, minutes: int,
                    provider: IsochroneProvider | None = None) -> tuple[str, tuple]:
    """SQL-предикат гео-фильтра для build_where(extra_sql=..., extra_params=...).
    Без провайдера — Precomputed-путь: круг по прямой (без сети).
    С провайдером — честный изохрон-полигон."""
    if provider is None:
        radius_m = minutes * WALK_SPEED_M_PER_MIN
        return ("ST_DWithin(geom::geography, "
                "ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
                (lon, lat, radius_m))
    poly = provider.isochrone(lon, lat, minutes)
    return ("ST_Within(geom, ST_SetSRID(ST_GeomFromGeoJSON(%s),4326))",
            (json.dumps(poly),))
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_geo_agent.py -v`
Expected: PASS (4 теста)

- [ ] **Step 5: Интеграционная проверка предиката на PostGIS (падающий тест)**

Добавить в `tests/test_geo_agent.py`:

```python
import psycopg
from habitus.db.init_db import init_db
from habitus.online.retrieval import filter_only_search
from habitus.online.schema import ParsedQuery


def test_point_predicate_filters_in_postgis():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            # NEAR ~200 м от точки, FAR ~5 км
            cur.execute("""INSERT INTO listings (external_id, source, geom) VALUES
                ('NEAR','test', ST_SetSRID(ST_MakePoint(37.6190, 55.7560), 4326)),
                ('FAR', 'test', ST_SetSRID(ST_MakePoint(37.70,   55.80),   4326));""")
        conn.commit()
        sql, params = point_predicate(37.6173, 55.7558, 15)   # радиус 1200 м
        cands = filter_only_search(conn, ParsedQuery(), geo_sql=sql,
                                   geo_params=params)
        assert [c.external_id for c in cands] == ["NEAR"]
```

- [ ] **Step 6: Запустить — PASS**

Run: `uv run pytest tests/test_geo_agent.py -v`
Expected: PASS (5 тестов)

- [ ] **Step 7: Commit**

```bash
git add habitus/online/geo.py tests/test_geo_agent.py
git commit -m "feat: гео-агент — IsochroneProvider, ORS-клиент, центральная точка, SQL-предикат"
```

---

### Task 6: Оркестратор — маршрутизация + relaxation loop

**Files:**
- Create: `habitus/online/orchestrator.py`
- Test: `tests/test_orchestrator.py`

**Interfaces:**
- Consumes: `ParsedQuery`, `GeoConstraint`, `PointConstraint` из `habitus.online.schema`; `hybrid_search(conn, pq, *, model=None, top_k=None, geo_sql=None, geo_params=(), query_vec=None, channels=("dense","sparse")) -> list[Candidate]`, `Candidate` из `habitus.online.retrieval`; `point_predicate(lon, lat, minutes, provider=None) -> tuple[str, tuple]`, `IsochroneProvider` из `habitus.online.geo`; `settings.min_results`, `settings.relaxation_max_iters`.
- Produces (на них опирается Task 8):
  - `relax(pq: ParsedQuery) -> tuple[ParsedQuery, str] | None` — один шаг ослабления по приоритету, `None` когда ослаблять нечего
  - `retrieve_with_relaxation(conn, pq: ParsedQuery, *, point: PointConstraint | None = None, provider: IsochroneProvider | None = None, model=None, query_vec=None, min_results: int | None = None, max_iters: int | None = None, search_fn=hybrid_search) -> tuple[list[Candidate], list[str], ParsedQuery]` — (кандидаты, список ослаблений, финальный ParsedQuery). `search_fn` инжектируется для юнит-тестов без БД.

Приоритет ослабления (из спеки 3.6): расширить `walk_minutes` (+5 мин, кап 30) → поднять `price_max` (+15%) → снять `window_orientation` → снять `noise_max`.

- [ ] **Step 1: Падающие юнит-тесты relax()**

`tests/test_orchestrator.py`:

```python
from datetime import datetime, timezone
from habitus.online.orchestrator import relax, retrieve_with_relaxation
from habitus.online.retrieval import Candidate
from habitus.online.schema import GeoConstraint, ParsedQuery, PointConstraint


def _cand(eid: str) -> Candidate:
    return Candidate(external_id=eid, doc_text="d", price=None, area=None,
                     rooms=None, facts={}, score=0.1,
                     updated_at=datetime(2026, 7, 1, tzinfo=timezone.utc))


def test_relax_order_geo_price_orientation_noise():
    pq = ParsedQuery(geo=[GeoConstraint(kind="metro", walk_minutes=10)],
                     price_max=10_000_000, window_orientation=["SW"],
                     noise_max="low")
    pq, n1 = relax(pq)
    assert pq.geo[0].walk_minutes == 15 and "metro" in n1

    # гео на капе → следующий приоритет: бюджет
    pq = pq.model_copy(update={"geo": [GeoConstraint(kind="metro",
                                                     walk_minutes=30)]})
    pq, n2 = relax(pq)
    assert pq.price_max == 11_500_000 and "+15%" in n2

    # бюджет убрали → снимается ориентация окон
    pq = pq.model_copy(update={"price_max": None})
    pq, n3 = relax(pq)
    assert pq.window_orientation == [] and "окон" in n3

    pq, n4 = relax(pq)
    assert pq.noise_max is None and "шум" in n4

    assert relax(pq) is None            # ослаблять больше нечего


def test_relax_geo_capped_at_30():
    pq = ParsedQuery(geo=[GeoConstraint(kind="school", walk_minutes=28)])
    pq2, note = relax(pq)
    assert pq2.geo[0].walk_minutes == 30 and "28→30" in note
    # на капе гео-шаг больше не применяется, а других фильтров нет
    assert relax(pq2) is None


def test_relaxation_loop_stops_when_enough():
    calls = []

    def fake_search(conn, pq, **kw):
        calls.append(pq)
        return [_cand(f"id{i}") for i in range(5)]

    cands, relaxed, final = retrieve_with_relaxation(
        None, ParsedQuery(semantic_text="x"), search_fn=fake_search,
        min_results=5)
    assert len(calls) == 1 and relaxed == [] and len(cands) == 5


def test_relaxation_loop_widens_until_max_iters():
    pq = ParsedQuery(geo=[GeoConstraint(kind="school", walk_minutes=10)])
    seen = []

    def fake_search(conn, q, **kw):
        seen.append(q)
        return []

    cands, relaxed, final = retrieve_with_relaxation(
        None, pq, search_fn=fake_search, min_results=1, max_iters=2)
    # исходный вызов + 2 ослабления: 10→15, 15→20
    assert [q.geo[0].walk_minutes for q in seen] == [10, 15, 20]
    assert len(relaxed) == 2 and final.geo[0].walk_minutes == 20


def test_relaxation_loop_stops_when_nothing_to_relax():
    def fake_search(conn, q, **kw):
        return []

    cands, relaxed, final = retrieve_with_relaxation(
        None, ParsedQuery(semantic_text="только семантика"),
        search_fn=fake_search, min_results=1, max_iters=5)
    assert cands == [] and relaxed == []


def test_custom_point_builds_geo_predicate():
    captured = {}

    def fake_search(conn, q, *, geo_sql=None, geo_params=(), **kw):
        captured["geo_sql"] = geo_sql
        captured["geo_params"] = geo_params
        return [_cand("A")] * 5

    retrieve_with_relaxation(None, ParsedQuery(semantic_text="x"),
                             point=PointConstraint(lon=37.6, lat=55.7, minutes=10),
                             search_fn=fake_search, min_results=1)
    assert "ST_DWithin" in captured["geo_sql"]
    assert captured["geo_params"] == (37.6, 55.7, 800.0)   # 10 мин * 80 м
```


- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_orchestrator.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.orchestrator`)

- [ ] **Step 3: Реализовать `habitus/online/orchestrator.py`**

```python
# habitus/online/orchestrator.py — маршрутизация + relaxation loop
from typing import Sequence

from habitus.config import settings
from habitus.online.geo import IsochroneProvider, point_predicate
from habitus.online.retrieval import Candidate, hybrid_search
from habitus.online.schema import GeoConstraint, ParsedQuery, PointConstraint

GEO_STEP_MIN = 5
GEO_CAP_MIN = 30
PRICE_RELAX = 1.15


def relax(pq: ParsedQuery) -> tuple[ParsedQuery, str] | None:
    """Один шаг ослабления по приоритету спеки. None — ослаблять нечего."""
    if pq.geo and any(g.walk_minutes < GEO_CAP_MIN for g in pq.geo):
        new_geo, notes = [], []
        for g in pq.geo:
            new_min = min(g.walk_minutes + GEO_STEP_MIN, GEO_CAP_MIN)
            if new_min != g.walk_minutes:
                notes.append(f"пешком до {g.kind}: {g.walk_minutes}→{new_min} мин")
            new_geo.append(GeoConstraint(kind=g.kind, walk_minutes=new_min))
        return pq.model_copy(update={"geo": new_geo}), "; ".join(notes)
    if pq.price_max is not None:
        new_price = int(pq.price_max * PRICE_RELAX)
        return (pq.model_copy(update={"price_max": new_price}),
                f"бюджет: {pq.price_max}→{new_price} (+15%)")
    if pq.window_orientation:
        return (pq.model_copy(update={"window_orientation": []}),
                "снят фильтр ориентации окон")
    if pq.noise_max is not None:
        return (pq.model_copy(update={"noise_max": None}),
                "снят фильтр уровня шума")
    return None


def retrieve_with_relaxation(
        conn, pq: ParsedQuery, *,
        point: PointConstraint | None = None,
        provider: IsochroneProvider | None = None,
        model=None, query_vec=None,
        min_results: int | None = None, max_iters: int | None = None,
        search_fn=hybrid_search) -> tuple[list[Candidate], list[str], ParsedQuery]:
    """Маршрутизация: кастомная точка → сначала гео-предикат, затем retrieval.
    Мало результатов → ослабляем и повторяем (каждый шаг — в relaxed)."""
    min_r = min_results if min_results is not None else settings.min_results
    iters = max_iters if max_iters is not None else settings.relaxation_max_iters

    geo_sql: str | None = None
    geo_params: Sequence = ()
    if point is not None:
        geo_sql, geo_params = point_predicate(point.lon, point.lat,
                                              point.minutes, provider)

    relaxed: list[str] = []
    cur_pq = pq
    cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                      geo_sql=geo_sql, geo_params=geo_params)
    for _ in range(iters):
        if len(cands) >= min_r:
            break
        step = relax(cur_pq)
        if step is None:
            break
        cur_pq, note = step
        relaxed.append(note)
        cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                          geo_sql=geo_sql, geo_params=geo_params)
    return cands, relaxed, cur_pq
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_orchestrator.py -v`
Expected: PASS (6 тестов)

- [ ] **Step 5: Commit**

```bash
git add habitus/online/orchestrator.py tests/test_orchestrator.py
git commit -m "feat: оркестратор — маршрутизация гео-точки и relaxation loop с трекингом ослаблений"
```

---

### Task 7: Объясняющая генерация — grounded-промпт, анти-галлюцинация, шаблон-фолбэк

**Files:**
- Create: `habitus/online/explain.py`
- Test: `tests/test_explain.py`

**Interfaces:**
- Consumes: `ResultItem` из `habitus.online.schema`; `LLMClient`, `FakeLLM`, `LLMResponse` из `habitus.online.llm`.
- Produces (на них опирается Task 8):
  - `GROUNDED_SYSTEM: str`
  - `facts_block(results: list[ResultItem], relaxed: list[str]) -> str`
  - `template_explanation(results: list[ResultItem], relaxed: list[str]) -> str`
  - `explain(query: str, results: list[ResultItem], relaxed: list[str], llm: LLMClient | None) -> tuple[str, bool]` — (текст, llm_ok). `llm_ok=False` ⇒ pipeline добавит `"llm"` в `degraded`.

- [ ] **Step 1: Падающие тесты grounding и фолбэка**

`tests/test_explain.py`:

```python
from habitus.online.explain import explain, facts_block, template_explanation
from habitus.online.llm import FakeLLM, LLMResponse
from habitus.online.schema import ResultItem


def _item(eid="A"):
    return ResultItem(external_id=eid, price=10_000_000, area=45.0, rooms=2,
                      address_facts={"walk_min_school": 8.0, "walk_min_metro": 6.0,
                                     "walk_min_park": None, "bar_density_500m": 0,
                                     "noise_level": "low",
                                     "window_orientation": ["SW"]},
                      score=0.9)


def test_facts_block_serializes_facts_and_relaxations():
    block = facts_block([_item()], ["бюджет: 10000000→11500000 (+15%)"])
    assert '"walk_min_school": 8.0' in block and '"id": "A"' in block
    assert "ОСЛАБЛЕНО: бюджет" in block


def test_explain_sends_only_facts_to_llm():
    fake = FakeLLM([LLMResponse(content="Тихий вариант, школа в 8 минутах.",
                                tool_arguments=None)])
    text, ok = explain("тихо и школа рядом", [_item()], [], fake)
    assert ok and text.startswith("Тихий")
    sys_msg = fake.calls[0]["messages"][0]["content"]
    user_msg = fake.calls[0]["messages"][-1]["content"]
    assert "ТОЛЬКО" in sys_msg and "Запрещено" in sys_msg   # анти-галлюцинация
    assert "ФАКТЫ" in user_msg and '"walk_min_school": 8.0' in user_msg
    assert fake.calls[0]["temperature"] == 0.0


def test_explain_no_llm_falls_back_to_template():
    text, ok = explain("q", [_item()], [], None)
    assert not ok
    assert "Найдено объектов: 1" in text and "школа в 8 мин" in text


def test_explain_llm_error_falls_back_to_template():
    text, ok = explain("q", [_item()], [], FakeLLM([]))   # ответы исчерпаны → ошибка
    assert not ok and "Найдено объектов: 1" in text


def test_template_mentions_relaxations_and_empty_results():
    text = template_explanation([], [])
    assert "ничего не найдено" in text.lower()
    text2 = template_explanation([_item()], ["снят фильтр уровня шума"])
    assert "снят фильтр уровня шума" in text2
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_explain.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.explain`)

- [ ] **Step 3: Реализовать `habitus/online/explain.py`**

```python
# habitus/online/explain.py — объяснение строго поверх фактов из БД
import json

from habitus.online.llm import LLMClient
from habitus.online.schema import ResultItem

GROUNDED_SYSTEM = """Ты — ассистент по недвижимости. Объясни пользователю подбор \
квартир по его запросу.
ЖЁСТКОЕ ПРАВИЛО: используй ТОЛЬКО данные из блока ФАКТЫ. Запрещено называть адреса, \
районы, станции метро, названия школ и любые сведения, которых нет в ФАКТАХ. \
Если каких-то данных нет — просто не упоминай их.
Если в ФАКТАХ есть строка «ОСЛАБЛЕНО», честно скажи, какие условия пришлось ослабить.
Отвечай на языке запроса пользователя, кратко: 3-6 предложений."""


def facts_block(results: list[ResultItem], relaxed: list[str]) -> str:
    """Факты для промпта: по JSON-строке на объект + строка ослаблений."""
    lines = [json.dumps({"id": r.external_id, "price": r.price, "area": r.area,
                         "rooms": r.rooms, **r.address_facts}, ensure_ascii=False)
             for r in results]
    if relaxed:
        lines.append("ОСЛАБЛЕНО: " + "; ".join(relaxed))
    return "\n".join(lines)


def template_explanation(results: list[ResultItem], relaxed: list[str]) -> str:
    """Деградация LLM: детерминированный ответ из тех же фактов."""
    if not results:
        return ("По заданным условиям ничего не найдено. "
                "Попробуйте ослабить фильтры.")
    parts = [f"Найдено объектов: {len(results)}."]
    top, f = results[0], results[0].address_facts
    bits = []
    if top.price is not None:
        bits.append(f"цена {top.price:,} ₽".replace(",", " "))
    if top.rooms is not None:
        bits.append(f"{top.rooms}-комн")
    if top.area is not None:
        bits.append(f"{top.area:.0f} м²")
    if f.get("walk_min_school") is not None:
        bits.append(f"школа в {f['walk_min_school']:.0f} мин пешком")
    if f.get("walk_min_metro") is not None:
        bits.append(f"метро в {f['walk_min_metro']:.0f} мин")
    if f.get("noise_level") == "low":
        bits.append("тихо")
    if f.get("bar_density_500m") == 0:
        bits.append("баров в радиусе 500 м нет")
    parts.append("Лучший вариант: " + ", ".join(bits) + ".")
    if relaxed:
        parts.append("Ослаблены условия: " + "; ".join(relaxed) + ".")
    return " ".join(parts)


def explain(query: str, results: list[ResultItem], relaxed: list[str],
            llm: LLMClient | None) -> tuple[str, bool]:
    """(текст, llm_ok). Любая ошибка LLM → шаблон, llm_ok=False."""
    if llm is None:
        return template_explanation(results, relaxed), False
    messages = [
        {"role": "system", "content": GROUNDED_SYSTEM},
        {"role": "user", "content":
         f"Запрос пользователя: {query}\n\nФАКТЫ:\n"
         f"{facts_block(results, relaxed)}\n\nОбъясни подбор."},
    ]
    try:
        resp = llm.complete(messages, temperature=0.0)
        if resp.content:
            return resp.content, True
    except Exception:
        pass
    return template_explanation(results, relaxed), False
```

- [ ] **Step 4: Запустить — PASS**

Run: `uv run pytest tests/test_explain.py -v`
Expected: PASS (5 тестов)

- [ ] **Step 5: Commit**

```bash
git add habitus/online/explain.py tests/test_explain.py
git commit -m "feat: grounded-объяснение поверх фактов БД с шаблонным фолбэком"
```

---

### Task 8: Сборка — pipeline (деградация по слоям), cache, trace, FastAPI, CLI search, smoke

**Files:**
- Create: `habitus/online/cache.py`
- Create: `habitus/online/trace.py`
- Create: `habitus/online/pipeline.py`
- Create: `habitus/online/service.py`
- Modify: `habitus/cli.py` (подкоманда `search`)
- Modify: `pyproject.toml` (`fastapi`, `uvicorn`; dev: `httpx`; опц. группа `trace`: `langfuse`)
- Test: `tests/test_cache.py`
- Test: `tests/test_pipeline.py`
- Test: `tests/test_service.py`
- Test: `tests/test_smoke_llm.py` (skip без `OPENROUTER_API_KEY`)

**Interfaces:**
- Consumes: всё из Tasks 1–7 ровно по их блокам Produces: `parse_query(text, llm, max_retries=3) -> ParsedQuery`, `ParseError`; `encode_query(text, model=None) -> tuple[list[float], dict[int, float]]`, `Candidate`, `hybrid_search`; `retrieve_with_relaxation(conn, pq, *, point=None, provider=None, model=None, query_vec=None, min_results=None, max_iters=None, search_fn=hybrid_search)`; `rerank(query, candidates, top_n=None, reranker=None)`; `explain(query, results, relaxed, llm) -> tuple[str, bool]`; `LLMUnavailable`; схемы; `get_conn()` из `habitus.db.connection`.
- Produces (на них опирается Task 9 и внешние потребители):
  - `cache.py`: `class LRUCache(maxsize: int = 256)` с методами `get(key_text: str) -> Any | None`, `put(key_text: str, value) -> None`, `clear() -> None`; синглтоны `embed_cache`, `parse_cache`, `explain_cache`.
  - `trace.py`: контекст-менеджер `span(name: str, **attrs)`.
  - `pipeline.py`: `to_result_item(c: Candidate) -> ResultItem`; `run_search(query: str, conn, *, llm: LLMClient | None = None, point: PointConstraint | None = None, provider: IsochroneProvider | None = None, model=None, reranker=None) -> SearchResponse`.
  - `service.py`: `app` (FastAPI), `POST /search` (`SearchRequest` → `SearchResponse`), `GET /health` → `{"status": "ok"}`.
  - CLI: `habitus search "<query>"`.

- [ ] **Step 1: Падающие тесты кэша**

`tests/test_cache.py`:

```python
from habitus.online.cache import LRUCache, embed_cache, explain_cache, parse_cache


def test_lru_get_put_by_text_hash():
    c = LRUCache(maxsize=2)
    assert c.get("нет") is None
    c.put("запрос", 42)
    assert c.get("запрос") == 42


def test_lru_evicts_oldest():
    c = LRUCache(maxsize=2)
    c.put("a", 1); c.put("b", 2)
    c.get("a")            # a — свежий
    c.put("c", 3)         # вытесняет b
    assert c.get("b") is None and c.get("a") == 1 and c.get("c") == 3


def test_lru_clear_and_singletons_exist():
    c = LRUCache()
    c.put("x", 1); c.clear()
    assert c.get("x") is None
    for cache in (embed_cache, parse_cache, explain_cache):
        assert isinstance(cache, LRUCache)
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_cache.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.cache`)

- [ ] **Step 3: Реализовать cache и trace**

`habitus/online/cache.py`:

```python
# habitus/online/cache.py — in-memory LRU по хэшу входного текста.
# Инвалидация не нужна: ключ детерминирован входом (спека 3.10).
from collections import OrderedDict
from typing import Any

from habitus.embed.document import content_hash


class LRUCache:
    def __init__(self, maxsize: int = 256):
        self.maxsize = maxsize
        self._d: OrderedDict[str, Any] = OrderedDict()

    def get(self, key_text: str) -> Any | None:
        k = content_hash(key_text)
        if k not in self._d:
            return None
        self._d.move_to_end(k)
        return self._d[k]

    def put(self, key_text: str, value) -> None:
        k = content_hash(key_text)
        self._d[k] = value
        self._d.move_to_end(k)
        if len(self._d) > self.maxsize:
            self._d.popitem(last=False)

    def clear(self) -> None:
        self._d.clear()


embed_cache = LRUCache()      # semantic_text → (dense, sparse)
parse_cache = LRUCache()      # текст запроса → ParsedQuery
explain_cache = LRUCache()    # запрос+ids выдачи → текст объяснения
```

`habitus/online/trace.py`:

```python
# habitus/online/trace.py — трейсинг шагов пайплайна: structlog-стиль +
# опциональный Langfuse (флаг settings.langfuse_enabled)
import logging
import time
from contextlib import contextmanager

from habitus.config import settings

log = logging.getLogger("habitus.trace")

_langfuse = None


def _lf():
    """Ленивый Langfuse-клиент; без флага/пакета — молча None."""
    global _langfuse
    if _langfuse is None and settings.langfuse_enabled:
        try:
            from langfuse import Langfuse
            _langfuse = Langfuse(host=settings.langfuse_host,
                                 public_key=settings.langfuse_public_key,
                                 secret_key=settings.langfuse_secret_key)
        except ImportError:
            log.warning("langfuse_enabled=True, но пакет langfuse не установлен")
    return _langfuse


@contextmanager
def span(name: str, **attrs):
    """Инструментация шага: parse → SQL → retrieval → rerank → generation."""
    t0 = time.perf_counter()
    try:
        yield
    finally:
        ms = (time.perf_counter() - t0) * 1000
        log.info("span=%s ms=%.1f %s", name, ms, attrs or "")
        lf = _lf()
        if lf is not None:
            lf.create_event(name=name, metadata={"ms": ms, **attrs})
```

- [ ] **Step 4: Запустить тесты кэша — PASS**

Run: `uv run pytest tests/test_cache.py -v`
Expected: PASS (3 теста)

- [ ] **Step 5: Commit cache+trace**

```bash
git add habitus/online/cache.py habitus/online/trace.py tests/test_cache.py
git commit -m "feat: LRU-кэш по хэшу текста и трейсинг шагов пайплайна"
```

- [ ] **Step 6: Падающие тесты pipeline (деградация по слоям, на dev-БД + фейках)**

`tests/test_pipeline.py`:

```python
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
    def compute_score(self, pairs, normalize=True):
        return [0.5] * len(pairs) if len(pairs) > 1 else 0.5


class BrokenReranker:
    def compute_score(self, pairs, normalize=True):
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
```

- [ ] **Step 7: Запустить — FAIL**

Run: `uv run pytest tests/test_pipeline.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.pipeline`)

- [ ] **Step 8: Реализовать `habitus/online/pipeline.py`**

```python
# habitus/online/pipeline.py — сборка end-to-end + деградация по слоям
from habitus.config import settings
from habitus.online import trace
from habitus.online.cache import embed_cache, explain_cache, parse_cache
from habitus.online.explain import explain
from habitus.online.geo import IsochroneProvider
from habitus.online.llm import LLMClient, LLMUnavailable
from habitus.online.nlu import ParseError, parse_query
from habitus.online.orchestrator import retrieve_with_relaxation
from habitus.online.rerank import rerank
from habitus.online.retrieval import Candidate, encode_query
from habitus.online.schema import (ParsedQuery, PointConstraint, ResultItem,
                                   SearchResponse)


def to_result_item(c: Candidate) -> ResultItem:
    return ResultItem(external_id=c.external_id, price=c.price, area=c.area,
                      rooms=c.rooms, address_facts=c.facts, score=c.score)


def run_search(query: str, conn, *, llm: LLMClient | None = None,
               point: PointConstraint | None = None,
               provider: IsochroneProvider | None = None,
               model=None, reranker=None) -> SearchResponse:
    degraded: list[str] = []

    # 1. NLU (кэш по хэшу текста; отказ → весь запрос в семантику)
    pq: ParsedQuery | None = parse_cache.get(query)
    if pq is None:
        if llm is None:
            pq = ParsedQuery(semantic_text=query)
            degraded.append("nlu")
        else:
            try:
                with trace.span("parse"):
                    pq = parse_query(query, llm)
                parse_cache.put(query, pq)
            except (ParseError, LLMUnavailable):
                pq = ParsedQuery(semantic_text=query)
                degraded.append("nlu")

    # 2. кодирование запроса (кэш; отказ → filter-only retrieval)
    query_vec = None
    search_pq = pq
    if pq.semantic_text:
        query_vec = embed_cache.get(pq.semantic_text)
        if query_vec is None:
            try:
                with trace.span("encode"):
                    query_vec = encode_query(pq.semantic_text, model=model)
                embed_cache.put(pq.semantic_text, query_vec)
            except Exception:
                degraded.append("vector")
                search_pq = pq.model_copy(update={"semantic_text": ""})

    # 3. retrieval + relaxation
    with trace.span("retrieval"):
        cands, relaxed, _ = retrieve_with_relaxation(
            conn, search_pq, point=point, provider=provider,
            model=model, query_vec=query_vec)

    # 4. rerank (отказ → порядок RRF)
    try:
        with trace.span("rerank", n=len(cands)):
            top = rerank(query, cands, reranker=reranker)
    except Exception:
        degraded.append("reranker")
        top = cands[: settings.rerank_top_n]

    results = [to_result_item(c) for c in top]

    # 5. объяснение (кэш по запросу+выдаче; отказ → шаблон)
    exp_key = query + "|" + ",".join(r.external_id for r in results)
    explanation = explain_cache.get(exp_key)
    if explanation is None:
        with trace.span("explain"):
            explanation, llm_ok = explain(query, results, relaxed, llm)
        if llm_ok:
            explain_cache.put(exp_key, explanation)
        else:
            degraded.append("llm")

    freshness = max((c.updated_at for c in top), default=None)
    data_freshness = (f"данные актуальны на {freshness:%Y-%m-%d %H:%M}"
                      if freshness else "нет данных")
    return SearchResponse(results=results, explanation=explanation, parsed=pq,
                          relaxed=relaxed, data_freshness=data_freshness,
                          degraded=degraded)
```

- [ ] **Step 9: Запустить — PASS**

Run: `uv run pytest tests/test_pipeline.py -v`
Expected: PASS (5 тестов)

- [ ] **Step 10: Commit pipeline**

```bash
git add habitus/online/pipeline.py tests/test_pipeline.py
git commit -m "feat: end-to-end пайплайн поиска с кэшами и деградацией по слоям"
```

- [ ] **Step 11: Падающий тест FastAPI**

Run: `uv add "fastapi>=0.115" "uvicorn>=0.30" && uv add --dev "httpx>=0.27"`

`tests/test_service.py`:

```python
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
```

- [ ] **Step 12: Запустить — FAIL**

Run: `uv run pytest tests/test_service.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.online.service`)

- [ ] **Step 13: Реализовать `habitus/online/service.py`**

```python
# habitus/online/service.py — тонкий FastAPI: валидация входа + вызов pipeline.
# Gateway/деплой — зона беков; бизнес-логики здесь нет.
from fastapi import FastAPI

from habitus.config import settings
from habitus.db.connection import get_conn
from habitus.online.pipeline import run_search
from habitus.online.schema import SearchRequest, SearchResponse

app = FastAPI(title="Habitus Search")


@app.get("/health")
def health() -> dict:
    return {"status": "ok"}


@app.post("/search", response_model=SearchResponse)
def search(req: SearchRequest) -> SearchResponse:
    llm = None
    if settings.openrouter_api_key:
        from habitus.online.llm import OpenRouterLLM
        llm = OpenRouterLLM()
    with get_conn() as conn:
        return run_search(req.query, conn, llm=llm, point=req.point)
```

- [ ] **Step 14: Запустить — PASS**

Run: `uv run pytest tests/test_service.py -v`
Expected: PASS (3 теста)

- [ ] **Step 15: CLI-подкоманда search + опциональная группа trace в pyproject**

В `pyproject.toml` добавить секцию (после `[project.scripts]`):

```toml
[project.optional-dependencies]
trace = ["langfuse>=2.50"]
```

В `habitus/cli.py`: добавить импорт `from habitus.config import settings` вверху, в `main()` после `sub.add_parser("update")`:

```python
    s = sub.add_parser("search")
    s.add_argument("query")
```

и ветку в `main()` после `elif args.cmd == "update":`:

```python
        elif args.cmd == "search":
            from habitus.online.llm import OpenRouterLLM
            from habitus.online.pipeline import run_search
            llm = OpenRouterLLM() if settings.openrouter_api_key else None
            resp = run_search(args.query, conn, llm=llm)
            for i, r in enumerate(resp.results, 1):
                print(f"{i}. {r.external_id} | {r.price} ₽ | {r.rooms}-комн | "
                      f"{r.area} м² | score={r.score:.3f}")
            print("\n" + resp.explanation)
            if resp.relaxed:
                print("Ослаблено: " + "; ".join(resp.relaxed))
            if resp.degraded:
                print("Деградация: " + ", ".join(resp.degraded))
            print(resp.data_freshness)
```

Проверка вручную (без ключа — деградация nlu/llm, но список и шаблон живые):

Run: `uv run habitus search "двушка"`
Expected: нумерованный список объектов + шаблонное объяснение + строка «данные актуальны на …»

- [ ] **Step 16: Smoke-тест на живом LLM (skip без ключа)**

`tests/test_smoke_llm.py`:

```python
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
```

- [ ] **Step 17: Запустить всё**

Run: `uv run pytest -v`
Expected: все зелёные; `tests/test_smoke_llm.py` — SKIPPED без ключа (или PASS при наличии `OPENROUTER_API_KEY`).

- [ ] **Step 18: Commit**

```bash
git add habitus/online/service.py habitus/cli.py tests/test_service.py tests/test_smoke_llm.py pyproject.toml uv.lock
git commit -m "feat: тонкий FastAPI /search и /health, CLI-подкоманда search, live-smoke на Qwen"
```

---

### Task 9: Eval-харнесс — golden-set, метрики, раннер, отчёт, CLI eval

**Files:**
- Create: `habitus/eval/__init__.py` (пустой)
- Create: `habitus/eval/metrics.py`
- Create: `habitus/eval/runner.py`
- Create: `habitus/eval/queries.yaml`
- Modify: `habitus/cli.py` (подкоманда `eval`)
- Modify: `pyproject.toml` (`pyyaml`)
- Test: `tests/test_eval.py`

**Interfaces:**
- Consumes: `parse_query(text, llm, max_retries=3) -> ParsedQuery`, `ParseError` из `habitus.online.nlu`; `hybrid_search(conn, pq, *, model=None, top_k=None, geo_sql=None, geo_params=(), query_vec=None, channels=("dense","sparse"))` из `habitus.online.retrieval`; `rerank(query, candidates, top_n=None, reranker=None)` из `habitus.online.rerank`; `ParsedQuery`; `LLMClient`/`FakeLLM`.
- Produces:
  - `metrics.py`: `parse_accuracy(expected: dict, got: ParsedQuery) -> float`, `recall_at_k(relevant: set[str], got: list[str], k: int = 10) -> float`, `ndcg_at_k(relevance: dict[str, float], got: list[str], k: int = 10) -> float`
  - `runner.py`: `DEFAULT_GOLDEN: Path` (= `habitus/eval/queries.yaml`), `load_golden(path: Path) -> list[dict]`, `run_eval(conn, llm: LLMClient | None, golden: list[dict], model=None, reranker=None) -> dict`, `format_report(res: dict) -> str` (markdown)
  - CLI: `habitus eval [--golden path]`

Формат записи golden-set (`queries.yaml`, список):

```yaml
- id: q01                 # уникальный id
  lang: ru                # ru|en
  query: "текст запроса"
  expected_parse:         # эталонный парс — сверяется по полям parse_accuracy
    price_max: 15000000
  relevant_ids: []        # опц.: размеченные external_id из dev-БД (recall@10)
  relevance: {}           # опц.: id → градация 0..3 (NDCG@10); пусто → бинарная из relevant_ids
```

- [ ] **Step 1: Падающие тесты метрик**

`tests/test_eval.py`:

```python
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
```

- [ ] **Step 2: Запустить — FAIL**

Run: `uv run pytest tests/test_eval.py -v`
Expected: FAIL (`ModuleNotFoundError: habitus.eval`)

- [ ] **Step 3: Реализовать `habitus/eval/metrics.py`**

`habitus/eval/__init__.py` — пустой файл.

```python
# habitus/eval/metrics.py — parse-accuracy, recall@10, NDCG@10
import math

from habitus.online.schema import ParsedQuery


def _norm(v):
    """Нормализация для сравнения: списки скаляров сортируем,
    списки dict (geo) сортируем по каноничному представлению."""
    if isinstance(v, list):
        if v and isinstance(v[0], dict):
            return sorted((sorted(d.items()) for d in v))
        return sorted(v)
    return v


def parse_accuracy(expected: dict, got: ParsedQuery) -> float:
    """Доля полей эталона, совпавших с фактическим парсом (field-level)."""
    if not expected:
        return 1.0
    dump = got.model_dump()
    hits = sum(1 for k, v in expected.items() if _norm(dump.get(k)) == _norm(v))
    return hits / len(expected)


def recall_at_k(relevant: set[str], got: list[str], k: int = 10) -> float:
    if not relevant:
        return 1.0
    return len(relevant & set(got[:k])) / len(relevant)


def ndcg_at_k(relevance: dict[str, float], got: list[str], k: int = 10) -> float:
    dcg = sum(relevance.get(x, 0.0) / math.log2(i + 2)
              for i, x in enumerate(got[:k]))
    ideal = sorted(relevance.values(), reverse=True)[:k]
    idcg = sum(r / math.log2(i + 2) for i, r in enumerate(ideal))
    return dcg / idcg if idcg else 0.0
```

- [ ] **Step 4: Запустить тесты метрик — PASS**

Run: `uv run pytest tests/test_eval.py -v`
Expected: PASS (4 теста)

- [ ] **Step 5: Commit метрик**

```bash
git add habitus/eval/ tests/test_eval.py
git commit -m "feat: метрики eval — parse-accuracy, recall@10, NDCG@10"
```

- [ ] **Step 6: Черновик golden-set (30 запросов, RU+EN)**

Run: `uv add "pyyaml>=6.0"`

`habitus/eval/queries.yaml` — первые 10 записей писать ровно так:

```yaml
- id: q01
  lang: ru
  query: "тихая двушка до 15 млн, школа в 10 минутах пешком, без баров"
  expected_parse:
    price_max: 15000000
    rooms: [2]
    geo: [{kind: school, walk_minutes: 10}]
    noise_max: low
    stop_factors: [bars]
  relevant_ids: []

- id: q02
  lang: ru
  query: "трёшка от 60 метров, окна на юго-запад, метро в 7 минутах"
  expected_parse:
    rooms: [3]
    area_min: 60.0
    window_orientation: [SW]
    geo: [{kind: metro, walk_minutes: 7}]
  relevant_ids: []

- id: q03
  lang: en
  query: "quiet flat near a strong school"
  expected_parse:
    noise_max: low
    geo: [{kind: school, walk_minutes: 15}]
    lang: en
  relevant_ids: []

- id: q04
  lang: ru
  query: "однушка до 9 млн, парк в 10 минутах"
  expected_parse:
    rooms: [1]
    price_max: 9000000
    geo: [{kind: park, walk_minutes: 10}]
  relevant_ids: []

- id: q05
  lang: ru
  query: "сталинка с высокими потолками, тихий зелёный двор"
  expected_parse:
    noise_max: low
  relevant_ids: []

- id: q06
  lang: en
  query: "2-room flat under 20 million rubles, metro within 5 minutes, windows to the west"
  expected_parse:
    rooms: [2]
    price_max: 20000000
    geo: [{kind: metro, walk_minutes: 5}]
    window_orientation: [W]
    lang: en
  relevant_ids: []

- id: q07
  lang: ru
  query: "работаем в Сколково и в Сити, нужен компромисс, тихий двор без баров"
  expected_parse:
    noise_max: low
    stop_factors: [bars]
  relevant_ids: []

- id: q08
  lang: ru
  query: "1-2 комнаты, 35-55 метров, бюджет от 10 до 14 млн"
  expected_parse:
    rooms: [1, 2]
    area_min: 35.0
    area_max: 55.0
    price_min: 10000000
    price_max: 14000000
  relevant_ids: []

- id: q09
  lang: en
  query: "family apartment, park and school walkable, low noise"
  expected_parse:
    geo: [{kind: park, walk_minutes: 15}, {kind: school, walk_minutes: 15}]
    noise_max: low
    lang: en
  relevant_ids: []

- id: q10
  lang: ru
  query: "тихо, юго-запад, школа рядом, без баров, бюджет бизнес"
  expected_parse:
    noise_max: low
    window_orientation: [SW]
    geo: [{kind: school, walk_minutes: 15}]
    stop_factors: [bars]
    price_max: 40000000
  relevant_ids: []
```

Дописать q11–q30 по матрице покрытия (по 4 запроса на категорию, в каждой категории минимум 1 EN):
1. только бюджет/комнаты/площадь (без гео и семантики);
2. только гео (school/metro/park, разные walk_minutes);
3. только семантика (образы: «двор-колодец», «лофт», «вид на реку», «high ceilings»);
4. стоп-факторы + шум (комбинации bars/noise_max);
5. смешанные «всё сразу» (фильтры+гео+семантика+стоп-факторы).

Для каждой записи `expected_parse` заполнять вручную по правилам SYSTEM_PROMPT из `habitus/online/nlu.py` (та же семантика полей). Это черновик — финальную правку делает пользователь (зафиксировано в спеке, раздел «Развилки»).

Разметка `relevant_ids` (после наполнения dev-БД реальными данными): для 5-10 запросов выбрать релевантные объекты вручную запросом вида

```sql
SELECT external_id, price, rooms, walk_min_school, noise_level,
       left(description, 120)
FROM listings
WHERE is_active AND price <= 15000000 AND rooms = 2
ORDER BY updated_at DESC LIMIT 30;
```

и вписать 3-5 подходящих `external_id` в `relevant_ids` записи. Пустой `relevant_ids` — запрос участвует только в parse-accuracy.

- [ ] **Step 7: Падающий тест раннера (FakeLLM, retrieval через relevant_ids на dev-БД)**

Добавить в `tests/test_eval.py`:

```python
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
```

- [ ] **Step 8: Запустить — FAIL**

Run: `uv run pytest tests/test_eval.py -v`
Expected: FAIL (`ImportError: habitus.eval.runner`)

- [ ] **Step 9: Реализовать `habitus/eval/runner.py`**

```python
# habitus/eval/runner.py — прогон golden-set: parse-accuracy + recall/NDCG
# с абляциями «dense-only vs RRF vs RRF+rerank» (слайд защиты)
from pathlib import Path

import yaml

from habitus.eval.metrics import ndcg_at_k, parse_accuracy, recall_at_k
from habitus.online.llm import LLMClient, LLMUnavailable
from habitus.online.nlu import ParseError, parse_query
from habitus.online.rerank import rerank
from habitus.online.retrieval import hybrid_search
from habitus.online.schema import ParsedQuery

DEFAULT_GOLDEN = Path(__file__).parent / "queries.yaml"

VARIANTS = {"dense": ("dense",), "rrf": ("dense", "sparse")}


def load_golden(path: Path) -> list[dict]:
    with open(path, encoding="utf-8") as f:
        return yaml.safe_load(f)


def _avg(xs: list[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def run_eval(conn, llm: LLMClient | None, golden: list[dict],
             model=None, reranker=None) -> dict:
    parse_scores: list[float] = []
    retr: dict[str, dict[str, list[float]]] = {
        v: {"recall": [], "ndcg": []} for v in (*VARIANTS, "rrf+rerank")}

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
        rel_map = {k: float(v) for k, v in (item.get("relevance") or {}).items()} \
            or {r: 1.0 for r in relevant}
        pq = ParsedQuery.model_validate(
            {**expected, "semantic_text":
             expected.get("semantic_text") or item["query"]})
        rrf_cands = None
        for name, channels in VARIANTS.items():
            cands = hybrid_search(conn, pq, model=model, channels=channels)
            ids = [c.external_id for c in cands]
            retr[name]["recall"].append(recall_at_k(relevant, ids))
            retr[name]["ndcg"].append(ndcg_at_k(rel_map, ids))
            if name == "rrf":
                rrf_cands = cands
        reranked = rerank(item["query"], rrf_cands, reranker=reranker)
        ids = [c.external_id for c in reranked]
        retr["rrf+rerank"]["recall"].append(recall_at_k(relevant, ids))
        retr["rrf+rerank"]["ndcg"].append(ndcg_at_k(rel_map, ids))

    return {
        "n_queries": len(golden),
        "parse_accuracy": _avg(parse_scores),
        "retrieval": {v: {"recall@10": _avg(s["recall"]),
                          "ndcg@10": _avg(s["ndcg"]),
                          "n": len(s["recall"])}
                      for v, s in retr.items()},
    }


def format_report(res: dict) -> str:
    lines = ["# Habitus eval", "",
             f"Запросов в golden-set: {res['n_queries']}",
             f"parse-accuracy: {res['parse_accuracy']:.2f}", "",
             "| вариант | recall@10 | NDCG@10 | n |",
             "|---|---|---|---|"]
    for name, m in res["retrieval"].items():
        lines.append(f"| {name} | {m['recall@10']:.2f} | "
                     f"{m['ndcg@10']:.2f} | {m['n']} |")
    return "\n".join(lines)
```

- [ ] **Step 10: Запустить — PASS**

Run: `uv run pytest tests/test_eval.py -v`
Expected: PASS (7 тестов)

- [ ] **Step 11: CLI-подкоманда eval**

В `habitus/cli.py`, в `main()` после блока `search`:

```python
    ev = sub.add_parser("eval")
    ev.add_argument("--golden", type=Path, default=None)
```

и ветка:

```python
        elif args.cmd == "eval":
            from habitus.eval.runner import (DEFAULT_GOLDEN, format_report,
                                             load_golden, run_eval)
            from habitus.online.llm import OpenRouterLLM
            llm = OpenRouterLLM() if settings.openrouter_api_key else None
            golden = load_golden(args.golden or DEFAULT_GOLDEN)
            print(format_report(run_eval(conn, llm, golden)))
```

Проверка вручную:

Run: `uv run habitus eval`
Expected: markdown-таблица с parse-accuracy (0.00 без ключа — LLM нет) и строками dense / rrf / rrf+rerank.

- [ ] **Step 12: Прогнать весь тест-сьют**

Run: `uv run pytest -v`
Expected: все зелёные (smoke — SKIPPED без ключа).

- [ ] **Step 13: Commit**

```bash
git add habitus/eval/ habitus/cli.py tests/test_eval.py pyproject.toml uv.lock
git commit -m "feat: eval-харнесс — golden-set, раннер с абляциями RRF/reranker, CLI eval"
```

---

## Порядок и процесс

- Последовательность задач: 1 → 2 → … → 9 (retrieval раньше NLU — тестируется на рукописных `ParsedQuery`; eval последним, черновик golden-set можно начать раньше для тюнинга промптов).
- По спеке (раздел 7): после каждого блока — ревью Opus → апрув → пуш.
- Критерии готовности (раздел 8 спеки): `habitus search "тихо, юго-запад, школа рядом, без баров, бюджет бизнес"` даёт осмысленный список + объяснение; английский запрос работает; `habitus eval` печатает метрики и дельту RRF/reranker; FastAPI живой; каждый слой деградирует, не убивая систему; все тесты зелёные.
