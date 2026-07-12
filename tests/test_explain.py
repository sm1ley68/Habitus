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
