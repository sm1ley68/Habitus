from habitus.config import settings
from habitus.online.explain import (GROUNDED_SYSTEM, cache_key, explain,
                                    facts_block, template_explanation)
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


def test_facts_block_carries_notes():
    # notes — честное покрытие низкозаполненного поля (пример: ориентация окон),
    # передаётся в блок ФАКТОВ так же, как ОСЛАБЛЕНО, и только оттуда
    note = "данные об ориентации окон есть у 64 из 3291 объявлений (1.9%) — учли как предпочтение, а не как фильтр"
    block = facts_block([_item()], [], notes=[note])
    assert f"ПРИМЕЧАНИЕ: {note}" in block


def test_facts_block_without_notes_has_no_note_line():
    block = facts_block([_item()], [])
    assert "ПРИМЕЧАНИЕ" not in block


def test_facts_block_caps_to_rerank_top_n(monkeypatch):
    # /search теперь отдаёт запас до settings.result_max_n (30) объектов для
    # пагинации шлюза — в промпт объяснения должны уехать только первые
    # settings.rerank_top_n, а не весь запас.
    monkeypatch.setattr(settings, "rerank_top_n", 2)
    items = [_item(f"X{i}") for i in range(5)]
    block = facts_block(items, [])
    ids_present = [f'"id": "X{i}"' in block for i in range(5)]
    assert ids_present == [True, True, False, False, False]


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


def test_explain_forwards_notes_to_llm():
    fake = FakeLLM([LLMResponse(content="Учли ориентацию как предпочтение.",
                                tool_arguments=None)])
    text, ok = explain("окна на юго-запад", [_item()], [], fake,
                       notes=["данные об ориентации окон есть у 64 из 3291 (1.9%)"])
    assert ok
    user_msg = fake.calls[0]["messages"][-1]["content"]
    assert "ПРИМЕЧАНИЕ: данные об ориентации окон есть у 64 из 3291" in user_msg


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


def test_facts_block_carries_address_and_station():
    item = ResultItem(external_id="A", price=20000000, area=54.0, rooms=2,
                      address_facts={"address": "Москва, Хамовники",
                                     "metro_station": "Парк культуры",
                                     "walk_min_metro": 7.0}, score=0.9)
    block = facts_block([item], [])
    assert "Хамовники" in block
    assert "Парк культуры" in block


def test_prompt_allows_address_but_still_forbids_invented_geography():
    # разрешение снимается ровно с двух grounded-полей — они названы по именам,
    # иначе тест не отличил бы разрешение от прежнего запрета «называть адреса»
    assert "address" in GROUNDED_SYSTEM
    assert "metro_station" in GROUNDED_SYSTEM
    assert "названия школ" in GROUNDED_SYSTEM   # запрет на названия школ остаётся


def test_prompt_mentions_notes_instruction():
    assert "ПРИМЕЧАНИЕ" in GROUNDED_SYSTEM


def test_cache_key_ignores_tail_beyond_page():
    # Объяснение считается по первым rerank_top_n объектам, значит и ключ кэша
    # обязан: одинаковая первая страница с разным хвостом — тот же текст, и
    # промахиваться мимо кэша не за что.
    head = [_item(f"A{i}") for i in range(settings.rerank_top_n)]
    tail_a = head + [_item("X1"), _item("X2")]
    tail_b = head + [_item("Y1")]
    assert cache_key("q", tail_a) == cache_key("q", tail_b) == cache_key("q", head)


def test_cache_key_distinguishes_different_pages():
    # Обратная сторона: разная первая страница — разный ключ.
    a = [_item(f"A{i}") for i in range(settings.rerank_top_n)]
    b = [_item(f"B{i}") for i in range(settings.rerank_top_n)]
    assert cache_key("q", a) != cache_key("q", b)
