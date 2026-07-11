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
