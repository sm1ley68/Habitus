import json
import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import SPARSE_DIM, to_sparsevec_literal
from habitus.online.cache import embed_cache, explain_cache, parse_cache
from habitus.online.llm import FakeLLM, LLMResponse
from habitus.online.pipeline import run_search
from habitus.online.schema import ParsedQuery

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


class CountingReranker:
    """Не скорит осмысленно — только запоминает размер пришедшего в rerank() пула."""
    def __init__(self):
        self.n_pairs: int | None = None

    def compute_score(self, pairs, normalize=True, max_length=None):
        self.n_pairs = len(pairs)
        if len(pairs) == 1:
            return 0.5                 # FlagReranker для одной пары — скаляр
        return [0.5] * len(pairs)


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


def test_area_center_filters(conn):
    # min_results=1: изолируем проверку строгого гео-фильтра от авто-расширения
    # (settings.min_results=5 иначе снял бы область — widen тестируется отдельно).
    with conn.cursor() as cur:
        cur.execute("UPDATE listings SET okrug='ЦАО' WHERE external_id='A';")
        cur.execute("UPDATE listings SET okrug='САО' WHERE external_id='B';")
    conn.commit()
    llm = FakeLLM([LLMResponse(content=None, tool_arguments=json.dumps(
        {"rooms": [2], "area": "центр", "semantic_text": "тихо"})), _explain_resp()])
    resp = run_search("двушка в центре", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker(), min_results=1)
    assert {r.external_id for r in resp.results} == {"A"}


def test_area_label_and_geojson_surface(conn):
    with conn.cursor() as cur:
        cur.execute("UPDATE listings SET okrug='ЦАО' WHERE external_id='A';")
        cur.execute("UPDATE listings SET okrug='САО' WHERE external_id='B';")
    conn.commit()
    llm = FakeLLM([LLMResponse(content=None, tool_arguments=json.dumps(
        {"rooms": [2], "area": "центр", "semantic_text": "тихо"})), _explain_resp()])
    resp = run_search("двушка в центре", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker(), min_results=1)
    assert resp.area_label and "ЦАО" in resp.area_label
    # admin_zones в fixture пуст → геометрия не собирается, но контракт-поле присутствует
    assert resp.area_geojson is None or resp.area_geojson["type"] == "FeatureCollection"


def test_no_area_no_label(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp()])   # без area
    resp = run_search("тихая двушка", conn, llm=llm, model=FakeModel(), reranker=FakeReranker())
    assert resp.area_label is None and resp.area_geojson is None


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


def test_search_does_not_return_other_cities(conn):
    with conn.cursor() as cur:
        cur.execute("""INSERT INTO listings (external_id, source, is_active, city,
                                             doc_text, geom)
            VALUES ('DXB1','t',TRUE,'dxb','квартира',
                    ST_SetSRID(ST_MakePoint(55.27,25.20),4326));""")
    conn.commit()
    resp = run_search("квартира", conn, city="msk")
    assert all(r.external_id != "DXB1" for r in resp.results)


def test_explain_disabled_leaves_explanation_empty(conn):
    # Шлюз забирает объяснение отдельным потоковым вызовом: второй LLM-вызов
    # внутри /search только задерживал бы выдачу объектов на 6–17 секунд.
    llm = FakeLLM([_parse_resp()])          # только NLU; explain дёрнул бы пустой фейк
    resp = run_search("тихая двушка", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker(), explain=False)

    assert resp.explanation == ""
    assert len(llm.calls) == 1              # explain не вызывался
    assert "llm" not in resp.degraded       # объяснения просто ещё нет, деградации нет


def test_explain_enabled_by_default(conn):
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    resp = run_search("тихая двушка", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker())

    assert resp.explanation == "Тихая двушка, школа рядом."


def test_rerank_pool_capped_to_rerank_pool_n(conn, monkeypatch):
    # fixture кладёт 2 объекта (A, B); pool_n=1 обязан урезать вход rerank()
    # до 1 пары, хотя кандидатов в retrieval-выдаче больше.
    monkeypatch.setattr(settings, "rerank_pool_n", 1)
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    cr = CountingReranker()
    run_search("тихая двушка", conn, llm=llm, model=FakeModel(), reranker=cr)
    assert cr.n_pairs == 1


def test_rerank_pool_capped_to_rerank_pool_n_with_geo(conn, monkeypatch):
    # Та же проверка на гео-ветке prefilter_pool — это ровно та ветка, где
    # code review нашёл систематический недобор пула (см. rerank.py). geo-клауза
    # в build_where фильтрует по walk_min_metro <= N — фикстура его не проставляет
    # (NULL), поэтому явно задаём, иначе retrieval вернёт 0 кандидатов и rerank
    # не позовётся вовсе.
    with conn.cursor() as cur:
        cur.execute("UPDATE listings SET walk_min_metro = 5;")
    conn.commit()
    monkeypatch.setattr(settings, "rerank_pool_n", 1)
    llm = FakeLLM([LLMResponse(content=None, tool_arguments=json.dumps(
        {"rooms": [2], "semantic_text": "тихо",
         "geo": [{"kind": "metro", "walk_minutes": 15}]})), _explain_resp()])
    cr = CountingReranker()
    run_search("тихая двушка у метро", conn, llm=llm, model=FakeModel(), reranker=cr)
    assert cr.n_pairs == 1


def test_timings_has_retrieval_stage(conn):
    resp = run_search("тихая двушка", conn, llm=None,
                      model=FakeModel(), reranker=FakeReranker())
    assert "retrieval" in resp.timings
    assert resp.timings["retrieval"] >= 0.0
    assert "parse" not in resp.timings   # llm=None → parse-стадия не выполнялась


def test_old_call_without_prev_parsed_is_unaffected(conn):
    # Страхует приёмочный пункт брифа: старый вызов без prev_parsed работает
    # ровно как раньше — intent по умолчанию "new_search", деградаций нет.
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    resp = run_search("тихая двушка", conn, llm=llm,
                      model=FakeModel(), reranker=FakeReranker())
    assert resp.intent == "new_search"
    assert resp.degraded == []
    assert resp.parsed.rooms == [2]


def test_run_search_with_prev_parsed_refine_searches_by_merged_query(conn):
    prev = ParsedQuery(price_max=20_000_000, rooms=[2], semantic_text="тихо")
    turn_resp = LLMResponse(content=None, tool_arguments=json.dumps(
        {"intent": "refine", "query": {"price_max": 12_000_000}},
        ensure_ascii=False))
    llm = FakeLLM([turn_resp, _explain_resp()])
    resp = run_search("подешевле", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker(), prev_parsed=prev)
    assert resp.intent == "refine"
    # price_max — из реплики, rooms/semantic_text унаследованы из prev
    assert resp.parsed.price_max == 12_000_000
    assert resp.parsed.rooms == [2]
    assert resp.parsed.semantic_text == "тихо"
    assert "nlu" not in resp.degraded
    assert resp.results        # фикстура (price=10 млн) проходит по слитому фильтру


def test_prev_parsed_bypasses_parse_cache(conn):
    # Кэш разбора ключуется одним текстом реплики, поэтому при multi-turn он
    # обязан быть выключен: одна и та же реплика с разным prev даёт разный
    # разбор, а закэшированный ответ подменил бы его чужим.
    prev_a = ParsedQuery(price_max=20_000_000, rooms=[2])
    prev_b = ParsedQuery(price_max=20_000_000, rooms=[3])
    turn_resp = LLMResponse(content=None, tool_arguments=json.dumps(
        {"intent": "refine", "query": {"price_max": 12_000_000}},
        ensure_ascii=False))

    llm_a = FakeLLM([turn_resp, _explain_resp()])
    resp_a = run_search("подешевле", conn, llm=llm_a, model=FakeModel(),
                        reranker=FakeReranker(), prev_parsed=prev_a)
    llm_b = FakeLLM([turn_resp, _explain_resp()])
    resp_b = run_search("подешевле", conn, llm=llm_b, model=FakeModel(),
                        reranker=FakeReranker(), prev_parsed=prev_b)

    assert resp_a.parsed.rooms == [2]
    assert resp_b.parsed.rooms == [3]     # не подменён кэшем от первого вызова
    assert parse_cache.get("подешевле") is None   # и в кэш ничего не легло


def test_no_llm_with_prev_parsed_degrades_to_new_search(conn):
    prev = ParsedQuery(price_max=20_000_000, rooms=[2])
    resp = run_search("подешевле", conn, llm=None, model=FakeModel(),
                      reranker=FakeReranker(), prev_parsed=prev)
    assert resp.intent == "new_search"
    assert "nlu" in resp.degraded
    assert resp.parsed.semantic_text == "подешевле"   # весь текст в семантику,
                                                       # prev не подмешан


# --- честное покрытие ориентации окон (Task 5) ------------------------------

def _orient_parse_resp():
    return LLMResponse(content=None, tool_arguments=json.dumps(
        {"window_orientation": ["SW"], "semantic_text": "тихо"},
        ensure_ascii=False))


def _run_orientation_search(conn, monkeypatch, coverage):
    """Прогон запроса с ориентацией; coverage подменяет подсчёт покрытия —
    либо парой (with_data, total), либо исключением."""
    def fake_coverage(_conn, _city):
        if isinstance(coverage, Exception):
            raise coverage
        return coverage
    monkeypatch.setattr("habitus.online.pipeline.orientation_coverage",
                        fake_coverage)
    llm = FakeLLM([_orient_parse_resp(), _explain_resp()])
    return run_search("окна на юго-запад", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker())


def test_orientation_note_reports_measured_coverage(conn, monkeypatch):
    resp = _run_orientation_search(conn, monkeypatch, (64, 3291))
    assert len(resp.notes) == 1
    note = resp.notes[0]
    assert "64" in note and "3291" in note and "1.9%" in note
    assert "предпочтение" in note


def test_orientation_note_absent_when_nothing_to_count(conn, monkeypatch):
    # total=0 — процент не из чего считать; выдуманного «0%» быть не должно
    resp = _run_orientation_search(conn, monkeypatch, (0, 0))
    assert resp.notes == []


def test_orientation_note_absent_when_count_fails(conn, monkeypatch):
    # сбой подсчёта не роняет поиск и не сочиняет цифру
    resp = _run_orientation_search(conn, monkeypatch,
                                   RuntimeError("БД недоступна"))
    assert resp.notes == []
    assert resp.results or resp.results == []      # поиск отработал, без 500


def test_no_orientation_in_query_no_note(conn):
    # заметка про покрытие появляется только когда пользователь про окна спросил
    llm = FakeLLM([_parse_resp(), _explain_resp()])
    resp = run_search("тихая двушка", conn, llm=llm, model=FakeModel(),
                      reranker=FakeReranker())
    assert resp.notes == []
