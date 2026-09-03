# tests/test_windows.py — экстракция стороны света окон из прозы описания
import json

import pytest

from habitus.clean.windows import (SYSTEM_PROMPT, WINDOW_TOOL,
                                   WindowParseError, extract_windows,
                                   lexical_support, parse_windows)
from habitus.online.llm import FakeLLM, LLMResponse


def _tool_resp(payload: dict) -> LLMResponse:
    return LLMResponse(content=None,
                       tool_arguments=json.dumps(payload, ensure_ascii=False))


def test_parse_windows_explicit():
    fake = FakeLLM([_tool_resp({"window_orientation": ["SW", "W"]})])
    assert parse_windows("окна на юго-запад и запад", fake) == ["SW", "W"]
    call = fake.calls[0]
    assert call["temperature"] == 0.0
    assert call["tools"][0]["function"]["name"] == "submit_window_orientation"


def test_parse_windows_no_mention_returns_empty():
    fake = FakeLLM([_tool_resp({"window_orientation": []})])
    assert parse_windows("уютная квартира с ремонтом", fake) == []


def test_parse_windows_invalid_enum_retries_then_fixed():
    fake = FakeLLM([
        _tool_resp({"window_orientation": ["ЮГ"]}),      # мимо enum
        _tool_resp({"window_orientation": ["S"]}),
    ])
    assert parse_windows("окна на юг", fake) == ["S"]
    # во 2-м вызове модели вернули текст ошибки валидации
    assert "не прошёл валидацию" in fake.calls[1]["messages"][-1]["content"]


def test_parse_windows_exhausted_raises():
    fake = FakeLLM([LLMResponse(content="мусор", tool_arguments=None)] * 3)
    with pytest.raises(WindowParseError):
        parse_windows("окна на юг", fake, max_retries=3)


def test_lexical_support_compound_not_singles():
    # «юго-запад» — это SW, а не S+W по подстрокам
    assert lexical_support("окна на юго-запад") == {"SW"}
    assert lexical_support("северо-восточная сторона") == {"NE"}


def test_lexical_support_astronomy_and_singles():
    assert lexical_support("закаты из гостиной") == {"W"}
    assert lexical_support("рассвет над парком") == {"E"}
    assert lexical_support("окна на юг и запад") == {"S", "W"}


def test_lexical_support_no_anchor():
    # «в сторону Кремля» и «солнечная» — не направления
    assert lexical_support("окна выходят в сторону Кремля, солнечная") == set()


def test_extract_windows_gates_hallucinated_dirs(conn_gate):
    # LLM вернул E,W для текста без якорей → строка остаётся NULL
    fake = FakeLLM([_tool_resp({"window_orientation": ["E", "W"]})])
    stats = extract_windows(conn_gate, fake)
    assert stats == {"checked": 1, "extracted": 0, "last_id": "G1"}
    with conn_gate.cursor() as cur:
        cur.execute("SELECT window_orientation FROM listings "
                    "WHERE external_id='G1';")
        assert cur.fetchone()[0] is None


def test_prompt_forbids_guessing():
    # принципиально: только явно написанное, «солнечная сторона» не угадывается
    assert "явно" in SYSTEM_PROMPT
    assert "закат" in SYSTEM_PROMPT and "рассвет" in SYSTEM_PROMPT
    assert "window_orientation" in json.dumps(
        WINDOW_TOOL["function"]["parameters"])


# --- батч по БД ---
import psycopg

from habitus.config import settings
from habitus.db.init_db import init_db


@pytest.fixture
def conn_gate():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, is_active,
                description) VALUES ('G1','test',TRUE,
                'Панорамные окна выходят в сторону Кремля.');""")
        c.commit()
        yield c


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            rows = [
                ("W1", "Светлая квартира, окна выходят на юг, закаты."),
                ("W2", "Просто хорошая квартира с ремонтом."),   # нет упоминаний
                ("W3", "Из окон виден рассвет над парком."),
            ]
            for eid, desc in rows:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active,
                           description) VALUES (%s,'test',TRUE,%s);""",
                    (eid, desc))
        c.commit()
        yield c


def test_extract_windows_updates_only_mentions(conn):
    # W2 без упоминаний окон — LLM для него даже не вызывается (префильтр)
    fake = FakeLLM([_tool_resp({"window_orientation": ["S", "W"]}),
                    _tool_resp({"window_orientation": ["E"]})])
    stats = extract_windows(conn, fake)
    assert stats == {"checked": 2, "extracted": 2, "last_id": "W3"}
    with conn.cursor() as cur:
        cur.execute("SELECT external_id, window_orientation FROM listings "
                    "ORDER BY external_id;")
        got = dict(cur.fetchall())
    assert got["W1"] == ["S", "W"] and got["W3"] == ["E"]
    assert got["W2"] is None                     # не трогали


def test_extract_windows_skips_failed_parse(conn):
    # первый объект не распарсился за все попытки — батч живёт дальше
    fake = FakeLLM([LLMResponse(content="мусор", tool_arguments=None)] * 3 +
                   [_tool_resp({"window_orientation": ["E"]})])
    stats = extract_windows(conn, fake)
    assert stats == {"checked": 2, "extracted": 1, "last_id": "W3"}


def test_extract_windows_idempotent(conn):
    fake = FakeLLM([_tool_resp({"window_orientation": ["S", "W"]}),
                    _tool_resp({"window_orientation": ["E"]})])
    extract_windows(conn, fake)
    # повторный прогон: заполненные строки не перепроверяются
    stats2 = extract_windows(conn, FakeLLM([]))
    assert stats2 == {"checked": 0, "extracted": 0, "last_id": None}


# --- возобновление пакета ---------------------------------------------------
# Пакет по 3000+ строк не переживает обрыв, если каждый повторный прогон
# начинается с начала: начало — это как раз строки, про которые уже известно,
# что стороны света в них нет (они остались с NULL и снова попадают в выборку).
# Прогон с --limit из-за этого перепроверял один и тот же непродуктивный
# префикс и не двигался вовсе.

def test_extract_windows_resumes_after_given_id(conn):
    fake = FakeLLM([_tool_resp({"window_orientation": ["E"]})])
    stats = extract_windows(conn, fake, after="W1")
    # W1 пропущен, обработан только W3 (W2 отсекает префильтр по упоминаниям)
    assert stats == {"checked": 1, "extracted": 1, "last_id": "W3"}
    with conn.cursor() as cur:
        cur.execute("SELECT window_orientation FROM listings WHERE external_id='W1';")
        assert cur.fetchone()[0] is None


def test_last_id_lets_the_next_chunk_move_forward(conn):
    """Ключевое свойство: два чанка по одной строке проходят РАЗНЫЕ строки.
    До появления last_id/after второй чанк повторял первый."""
    first = extract_windows(conn, FakeLLM([_tool_resp({"window_orientation": []})]),
                            limit=1)
    assert first["last_id"] == "W1"
    second = extract_windows(conn, FakeLLM([_tool_resp({"window_orientation": ["E"]})]),
                             limit=1, after=first["last_id"])
    assert second["last_id"] == "W3"
    assert second["checked"] == 1


def test_limit_without_after_repeats_the_same_unproductive_prefix(conn):
    """Так это выглядело как баг: строка, не давшая результата, остаётся с NULL
    и снова возглавляет выборку. Тест фиксирует, что лечится это именно
    параметром after, а не изменением выборки."""
    first = extract_windows(conn, FakeLLM([_tool_resp({"window_orientation": []})]),
                            limit=1)
    again = extract_windows(conn, FakeLLM([_tool_resp({"window_orientation": []})]),
                            limit=1)
    assert first["last_id"] == again["last_id"] == "W1"
