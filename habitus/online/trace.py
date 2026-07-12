# habitus/online/trace.py — трейсинг шагов пайплайна: structlog-стиль +
# опциональный Langfuse (флаг settings.langfuse_enabled)
import logging
import time
from contextlib import contextmanager

from habitus.config import settings

log = logging.getLogger("habitus.trace")

_langfuse = None


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
