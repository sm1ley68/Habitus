import asyncio

import pytest
from habitus.config import settings
from habitus.online.llm import (AsyncOpenRouterLLM, FakeLLM, FakeStreamLLM,
                                LLMResponse, LLMUnavailable, OpenRouterLLM)


def test_config_has_online_fields():
    assert settings.llm_base_url == "https://openrouter.ai/api/v1"
    assert settings.reranker_model == "BAAI/bge-reranker-v2-m3"
    # rrf_k=40 is the current golden-set-tuned value (see config comment).
    assert settings.rrf_k == 40 and settings.retrieval_top_k == 100
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


class _FakeChunk:
    """Кадр стрима openai-SDK: choices[0].delta.content."""
    def __init__(self, content):
        delta = type("D", (), {"content": content})()
        self.choices = [type("C", (), {"delta": delta})()]


class _FakeAsyncStream:
    """Асинхронный итератор чанков; элемент-исключение поднимается на месте."""
    def __init__(self, chunks):
        self._chunks = list(chunks)

    def __aiter__(self):
        return self

    async def __anext__(self):
        if not self._chunks:
            raise StopAsyncIteration
        chunk = self._chunks.pop(0)
        if isinstance(chunk, Exception):
            raise chunk
        return _FakeChunk(chunk)


class _FakeAsyncOpenAI:
    """Скриптованный async-клиент: по сценарию на модель, в порядке вызовов."""
    def __init__(self, scripts):
        self.scripts = list(scripts)
        self.models_tried = []
        chat = type("Chat", (), {})()
        chat.completions = self
        self.chat = chat

    async def create(self, *, model, messages, temperature, stream=False, **kw):
        self.models_tried.append(model)
        script = self.scripts.pop(0)
        if isinstance(script, Exception):
            raise script
        return _FakeAsyncStream(script)


def _drain(llm, client=None):
    async def _run():
        return [c async for c in llm.stream([{"role": "user", "content": "q"}])]
    return asyncio.run(_run())


def test_fake_stream_llm_yields_chunks_and_records_calls():
    fake = FakeStreamLLM(["раз ", "два"])
    assert _drain(fake) == ["раз ", "два"]
    assert fake.calls[0]["messages"][0]["content"] == "q"
    assert fake.calls[0]["temperature"] == 0.0


def test_stream_falls_back_to_next_model_before_first_chunk():
    client = _FakeAsyncOpenAI([TimeoutError("primary down"), ["ответ"]])
    llm = AsyncOpenRouterLLM(client=client)

    assert _drain(llm) == ["ответ"]
    assert client.models_tried == [settings.llm_model, settings.llm_fallbacks[0]]


def test_stream_does_not_switch_model_after_first_chunk():
    # Перезапуск на другой модели после отданного текста склеил бы два разных
    # объяснения в одно — обрыв должен дойти до вызывающего как ошибка.
    client = _FakeAsyncOpenAI([["Тихий ", RuntimeError("оборвалось")], ["второй"]])
    llm = AsyncOpenRouterLLM(client=client)

    async def _run():
        got = []
        with pytest.raises(RuntimeError):
            async for chunk in llm.stream([{"role": "user", "content": "q"}]):
                got.append(chunk)
        return got

    assert asyncio.run(_run()) == ["Тихий "]
    assert client.models_tried == [settings.llm_model]


def test_stream_all_models_down_raises_llm_unavailable():
    down = [TimeoutError("down")] * (1 + len(settings.llm_fallbacks))
    llm = AsyncOpenRouterLLM(client=_FakeAsyncOpenAI(down))

    with pytest.raises(LLMUnavailable):
        _drain(llm)


def test_stream_skips_empty_deltas():
    client = _FakeAsyncOpenAI([[None, "текст", None]])
    llm = AsyncOpenRouterLLM(client=client)

    assert _drain(llm) == ["текст"]
