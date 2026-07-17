import pytest
from habitus.config import settings
from habitus.online.llm import FakeLLM, LLMResponse, LLMUnavailable, OpenRouterLLM


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
