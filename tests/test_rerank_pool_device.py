"""Пул реранка на машине без CUDA.

Кросс-энкодер линеен по числу пар, и вне CUDA он идёт на CPU (см. get_reranker).
На слабой машине пул в 40 пар — это десятки секунд: шлюз столько не ждёт и рвёт
запрос по таймауту, хотя ML позже честно досчитывает. Поэтому дефолтный пул вне
CUDA режется до rerank_pool_n_cpu.

Значение 25 не выдумано: docs/notes/rerank-pool-2026-08-18.md меряет 25/40/60/100
на одном эталоне — recall@10 держится (0.34 против 0.34 у сотни), проседает лишь
MRR (0.55 → 0.48). Это осознанный размен «голова выдачи чуть хуже упорядочена»
против «поиск вообще не отвечает».

Потолок — дефолт для ненастроенной машины, а не запрет: явный пул уважается,
иначе сломался бы инструмент замеров (`RERANK_POOL_N=100 uv run habitus eval`).
"""
from datetime import datetime, timezone

import habitus.online.rerank as rerank_mod
from habitus.config import settings
from habitus.online.rerank import effective_pool_n, prefilter_pool
from habitus.online.retrieval import Candidate
from habitus.online.schema import ParsedQuery


def _candidates(n: int) -> list[Candidate]:
    now = datetime(2026, 8, 25, tzinfo=timezone.utc)
    return [Candidate(external_id=f"c{i}", doc_text=f"объявление {i}",
                      price=12_000_000, area=54.0, rooms=2, facts={},
                      score=1.0 - i / 100, updated_at=now)
            for i in range(n)]


def _unconfigured(monkeypatch):
    """Машина, где RERANK_POOL_N никто не задавал."""
    monkeypatch.setattr(rerank_mod, "_pool_configured", lambda: False)


def test_default_pool_capped_without_cuda(monkeypatch):
    _unconfigured(monkeypatch)
    monkeypatch.setattr(rerank_mod, "_cuda_available", lambda: False)
    assert effective_pool_n() == settings.rerank_pool_n_cpu


def test_default_pool_untouched_on_cuda(monkeypatch):
    _unconfigured(monkeypatch)
    monkeypatch.setattr(rerank_mod, "_cuda_available", lambda: True)
    assert effective_pool_n() == settings.rerank_pool_n


def test_explicit_argument_wins_over_cap(monkeypatch):
    """Аргумент вызывающего сильнее потолка — на нём стоят тесты и eval."""
    _unconfigured(monkeypatch)
    monkeypatch.setattr(rerank_mod, "_cuda_available", lambda: False)
    assert effective_pool_n(100) == 100


def test_operator_setting_wins_over_cap(monkeypatch):
    """Заданный RERANK_POOL_N уважается: иначе замеры молча мерили бы не то."""
    monkeypatch.setattr(rerank_mod, "_cuda_available", lambda: False)
    monkeypatch.setattr(rerank_mod, "_pool_configured", lambda: True)
    monkeypatch.setattr(settings, "rerank_pool_n", 100, raising=False)
    assert effective_pool_n() == 100


def test_prefilter_pool_respects_cap(monkeypatch):
    _unconfigured(monkeypatch)
    monkeypatch.setattr(rerank_mod, "_cuda_available", lambda: False)
    pool = prefilter_pool(ParsedQuery(semantic_text="тихая двушка"), _candidates(60))
    assert len(pool) == settings.rerank_pool_n_cpu


def test_prefilter_pool_full_on_cuda(monkeypatch):
    _unconfigured(monkeypatch)
    monkeypatch.setattr(rerank_mod, "_cuda_available", lambda: True)
    pool = prefilter_pool(ParsedQuery(semantic_text="тихая двушка"), _candidates(60))
    assert len(pool) == settings.rerank_pool_n
