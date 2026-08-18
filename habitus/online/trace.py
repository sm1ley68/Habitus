# habitus/online/trace.py — трейсинг шагов пайплайна: structlog-стиль +
# опциональный Langfuse (флаг settings.langfuse_enabled)
import functools
import logging
import time
from contextlib import contextmanager
from contextvars import ContextVar

from habitus.config import settings

log = logging.getLogger("habitus.trace")

_langfuse = None

# Активный сборщик таймингов текущего вызова (для SearchResponse.timings).
# ContextVar, а не глобальная переменная: FastAPI гоняет запросы в пуле
# потоков, и разные вызовы run_search не должны писать друг другу в один словарь.
# По умолчанию None — существующие `with trace.span(...):` по всему проекту
# продолжают просто логировать, ничего никуда не собирая.
_active_sink: ContextVar["dict[str, float] | None"] = ContextVar(
    "_active_sink", default=None)


@contextmanager
def collector():
    """Открыть окно сбора: вложенные span() в этом контексте пишут мс сюда же.

    Возвращает словарь по ссылке — заполняется по ходу выполнения тела `with`,
    читать его нужно после выхода из span'ов, а не до.
    """
    sink: dict[str, float] = {}
    token = _active_sink.set(sink)
    try:
        yield sink
    finally:
        _active_sink.reset(token)


def with_timings(fn):
    """Декоратор для run_search: открывает collector() вокруг всего вызова и
    проставляет собранные мс в `.timings` результата (если у него есть такое
    поле). Декоратор, а не `with collector():` внутри тела — оборачивание тела
    run_search в with означало бы переотступить всю функцию ради одного поля
    в конце, раздув диф шагом, который этого не требует.
    """
    @functools.wraps(fn)
    def _wrapped(*args, **kwargs):
        with collector() as sink:
            result = fn(*args, **kwargs)
        if hasattr(result, "timings"):
            result.timings = sink
        return result
    return _wrapped


def _lf():
    """Ленивый Langfuse-клиент; без флага/пакета — молча None."""
    global _langfuse
    if _langfuse is None and settings.langfuse_enabled:
        try:
            from langfuse import Langfuse
            _langfuse = Langfuse(host=settings.langfuse_host,
                                 public_key=settings.langfuse_public_key,
                                 secret_key=settings.langfuse_secret_key)
        except ImportError:
            log.warning("langfuse_enabled=True, но пакет langfuse не установлен")
    return _langfuse


@contextmanager
def span(name: str, **attrs):
    """Инструментация шага: parse → SQL → retrieval → rerank → generation."""
    t0 = time.perf_counter()
    try:
        yield
    finally:
        ms = (time.perf_counter() - t0) * 1000
        log.info("span=%s ms=%.1f %s", name, ms, attrs or "")
        lf = _lf()
        if lf is not None:
            lf.create_event(name=name, metadata={"ms": ms, **attrs})
        sink = _active_sink.get()
        if sink is not None:
            sink[name] = ms
