"""Потоковое объяснение: токены идут по мере генерации, деградация честная.

Синхронный `explain` остаётся для CLI и eval; шлюз ходит за `explain_stream`,
чтобы пользователь видел текст с первой секунды, а не после полного ответа LLM.
"""
import asyncio

from habitus.online.explain import (GROUNDED_SYSTEM, explain_stream,
                                    template_explanation)
from habitus.online.llm import FakeStreamLLM
from habitus.online.schema import ResultItem


def _item(eid="A"):
    return ResultItem(external_id=eid, price=10_000_000, area=45.0, rooms=2,
                      address_facts={"walk_min_school": 8.0, "walk_min_metro": 6.0,
                                     "noise_level": "low", "bar_density_500m": 0},
                      score=0.9)


def collect(agen):
    """Асинхронный генератор → список событий (pytest-asyncio в проекте нет)."""
    async def _run():
        return [event async for event in agen]
    return asyncio.run(_run())


def tokens_of(events):
    return "".join(e["token"] for e in events if "token" in e)


def test_stream_yields_tokens_then_terminal_done():
    fake = FakeStreamLLM(["Тихий ", "вариант, ", "школа в 8 минутах."])
    events = collect(explain_stream("тихо и школа рядом", [_item()], [], fake))

    assert tokens_of(events) == "Тихий вариант, школа в 8 минутах."
    assert events[-1] == {"done": True, "llm_ok": True}
    assert all("token" in e for e in events[:-1])


def test_stream_sends_only_facts_to_llm():
    fake = FakeStreamLLM(["ок"])
    collect(explain_stream("тихо", [_item()], [], fake))

    sys_msg = fake.calls[0]["messages"][0]["content"]
    user_msg = fake.calls[0]["messages"][-1]["content"]
    assert sys_msg == GROUNDED_SYSTEM
    assert "ФАКТЫ" in user_msg and '"walk_min_school": 8.0' in user_msg
    assert fake.calls[0]["temperature"] == 0.0


def test_stream_without_llm_falls_back_to_template():
    events = collect(explain_stream("q", [_item()], [], None))

    assert tokens_of(events) == template_explanation([_item()], [])
    assert events[-1] == {"done": True, "llm_ok": False}


def test_stream_failure_before_first_token_falls_back_to_template():
    fake = FakeStreamLLM([RuntimeError("primary down")])
    events = collect(explain_stream("q", [_item()], [], fake))

    assert tokens_of(events) == template_explanation([_item()], [])
    assert events[-1] == {"done": True, "llm_ok": False}


def test_stream_failure_midway_keeps_delivered_text_and_marks_degradation():
    # Шаблон дописывать поверх уже отданного текста нельзя — получилось бы
    # два объяснения подряд. Отдаём что успели и честно помечаем деградацию.
    fake = FakeStreamLLM(["Тихий ", "вариант", RuntimeError("оборвалось")])
    events = collect(explain_stream("q", [_item()], [], fake))

    assert tokens_of(events) == "Тихий вариант"
    assert events[-1] == {"done": True, "llm_ok": False}


def test_stream_relaxations_reach_the_prompt():
    fake = FakeStreamLLM(["ок"])
    collect(explain_stream("q", [_item()], ["бюджет: 10000000→11500000 (+15%)"], fake))

    assert "ОСЛАБЛЕНО: бюджет" in fake.calls[0]["messages"][-1]["content"]
