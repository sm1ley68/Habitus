import json
import pytest
from habitus.online.llm import FakeLLM, LLMResponse
from habitus.online.nlu import (PARSE_TOOL, SYSTEM_PROMPT, ParseError,
                                merge_parsed, parse_query, parse_turn)
from habitus.online.schema import ParsedQuery, ParsedTurn


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


def test_parse_query_extracts_area():
    fake = FakeLLM([_tool_resp({"rooms": [2], "area": "север",
                                "noise_max": "low", "semantic_text": "с собакой"})])
    pq = parse_query("двушка на севере в тихом районе, гулять с собакой", fake)
    assert pq.area == "север" and pq.rooms == [2]
    assert "север" not in pq.semantic_text          # ушло в area, не в семантику


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


def test_system_prompt_covers_district_and_named_zone_examples():
    from habitus.online.nlu import SYSTEM_PROMPT
    # примеры района и именованного места ориентируют модель на area, а не semantic_text
    assert "Хамовники" in SYSTEM_PROMPT
    assert "Патрики" in SYSTEM_PROMPT


# --- parse_turn / merge_parsed (многоходовый чат) ---------------------------


def test_parse_turn_without_prev_equivalent_to_parse_query():
    fake = FakeLLM([_tool_resp({"price_max": 15_000_000, "rooms": [2],
                                "semantic_text": "тихо"})])
    turn = parse_turn("тихая двушка до 15 млн", fake, None)
    assert turn.intent == "new_search"
    assert turn.cleared_fields == []
    assert turn.query.price_max == 15_000_000
    assert turn.query.rooms == [2]
    assert turn.query.semantic_text == "тихо"
    # промпт и tool-схема — те же, что у parse_query (submit_parsed_query, а
    # не submit_parsed_turn: классифицировать intent без prev нечего)
    call = fake.calls[0]
    assert call["tools"][0]["function"]["name"] == "submit_parsed_query"
    # ровно SYSTEM_PROMPT: правила multi-turn в эту ветку подмешиваться не должны
    assert call["messages"][0]["content"] == SYSTEM_PROMPT


def test_parse_turn_with_prev_uses_turn_tool_and_prev_in_prompt():
    prev = ParsedQuery(price_max=20_000_000, rooms=[2])
    fake = FakeLLM([_tool_resp({"intent": "refine",
                                "query": {"price_max": 15_000_000}})])
    turn = parse_turn("подешевле", fake, prev)
    assert turn.intent == "refine"
    assert turn.query.price_max == 15_000_000
    call = fake.calls[0]
    assert call["tools"][0]["function"]["name"] == "submit_parsed_turn"
    assert "20000000" in call["messages"][0]["content"]   # JSON prev в промпте


def test_parse_turn_unknown_cleared_field_does_not_raise():
    prev = ParsedQuery(price_max=20_000_000)
    fake = FakeLLM([_tool_resp({"intent": "refine",
                                "cleared_fields": ["price_max", "not_a_field"]})])
    turn = parse_turn("неважно бюджет", fake, prev)
    assert turn.cleared_fields == ["price_max"]


def test_merge_parsed_refine_changes_only_sent_field():
    prev = ParsedQuery(price_max=20_000_000, rooms=[2], noise_max="low")
    turn = ParsedTurn(intent="refine", query=ParsedQuery(price_max=15_000_000))
    merged = merge_parsed(prev, turn)
    assert merged.price_max == 15_000_000
    assert merged.rooms == [2]           # не тронуто
    assert merged.noise_max == "low"     # не тронуто


def test_merge_parsed_cleared_field_resets_to_default():
    prev = ParsedQuery(price_max=20_000_000, noise_max="low")
    turn = ParsedTurn(intent="refine", cleared_fields=["noise_max"])
    merged = merge_parsed(prev, turn)
    assert merged.noise_max is None
    assert merged.price_max == 20_000_000    # прочее не тронуто


def test_merge_parsed_new_search_ignores_prev():
    prev = ParsedQuery(price_max=20_000_000, rooms=[2], noise_max="low")
    turn = ParsedTurn(intent="new_search", query=ParsedQuery(rooms=[3]))
    merged = merge_parsed(prev, turn)
    assert merged == ParsedQuery(rooms=[3])
    assert merged.price_max is None
    assert merged.noise_max is None


def test_merge_parsed_empty_semantic_text_keeps_prev():
    prev = ParsedQuery(semantic_text="тихий двор-колодец")
    turn = ParsedTurn(intent="refine", query=ParsedQuery(semantic_text=""))
    merged = merge_parsed(prev, turn)
    assert merged.semantic_text == "тихий двор-колодец"


def test_merge_parsed_nonempty_semantic_text_replaces_prev():
    prev = ParsedQuery(semantic_text="тихий двор-колодец")
    turn = ParsedTurn(intent="refine", query=ParsedQuery(semantic_text="с видом"))
    merged = merge_parsed(prev, turn)
    assert merged.semantic_text == "с видом"


def test_merge_parsed_explicit_lang_replaces_prev():
    prev = ParsedQuery(lang="ru")
    turn = ParsedTurn(intent="refine", query=ParsedQuery(lang="en"))
    merged = merge_parsed(prev, turn)
    assert merged.lang == "en"


def test_merge_parsed_absent_lang_keeps_prev():
    # Промпт правки велит присылать только изменившиеся поля, поэтому
    # отсутствие lang в реплике не должно откатывать сессию на дефолтный "ru".
    prev = ParsedQuery(lang="en", price_max=20_000_000)
    turn = ParsedTurn.model_validate({"intent": "refine",
                                      "query": {"price_max": 15_000_000}})
    merged = merge_parsed(prev, turn)
    assert merged.lang == "en"


def test_merge_parsed_empty_list_does_not_clear_prev_rooms():
    # Пустой список от LLM — не «сбросить фильтр»: для сброса есть cleared_fields.
    prev = ParsedQuery(rooms=[2], window_orientation=["SW"], stop_factors=["bars"])
    turn = ParsedTurn(intent="refine",
                      query=ParsedQuery(rooms=[], window_orientation=[],
                                        stop_factors=[]))
    merged = merge_parsed(prev, turn)
    assert merged.rooms == [2]
    assert merged.window_orientation == ["SW"]
    assert merged.stop_factors == ["bars"]


def test_merge_parsed_cleared_field_still_resets_list_field():
    # Обратная сторона предыдущего теста: явный сброс списка обязан работать.
    prev = ParsedQuery(rooms=[2])
    turn = ParsedTurn(intent="refine", cleared_fields=["rooms"])
    merged = merge_parsed(prev, turn)
    assert merged.rooms is None


def test_merge_parsed_followup_keeps_prev_unchanged():
    # На эту ветку опирается шлюз: followup — вопрос про уже показанную выдачу,
    # параметры поиска меняться не должны.
    prev = ParsedQuery(price_max=20_000_000, rooms=[2], noise_max="low",
                       semantic_text="тихий двор", lang="en")
    turn = ParsedTurn(intent="followup")
    merged = merge_parsed(prev, turn)
    assert merged == prev
