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
