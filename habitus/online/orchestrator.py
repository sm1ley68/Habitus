# habitus/online/orchestrator.py — маршрутизация + relaxation loop
from typing import Sequence

from habitus.config import settings
from habitus.online.geo import (IsochroneProvider, point_predicate,
                                 resolve_area)
from habitus.online.retrieval import Candidate, hybrid_search
from habitus.online.schema import GeoConstraint, ParsedQuery, PointConstraint

GEO_STEP_MIN = 5
GEO_CAP_MIN = 30
PRICE_RELAX = 1.15


def relax(pq: ParsedQuery) -> tuple[ParsedQuery, str] | None:
    """Один шаг ослабления по приоритету спеки. None — ослаблять нечего."""
    if pq.geo and any(g.walk_minutes < GEO_CAP_MIN for g in pq.geo):
        new_geo, notes = [], []
        for g in pq.geo:
            new_min = min(g.walk_minutes + GEO_STEP_MIN, GEO_CAP_MIN)
            if new_min != g.walk_minutes:
                notes.append(f"пешком до {g.kind}: {g.walk_minutes}→{new_min} мин")
            new_geo.append(GeoConstraint(kind=g.kind, walk_minutes=new_min))
        return pq.model_copy(update={"geo": new_geo}), "; ".join(notes)
    if pq.price_max is not None:
        new_price = int(pq.price_max * PRICE_RELAX)
        return (pq.model_copy(update={"price_max": new_price}),
                f"бюджет: {pq.price_max}→{new_price} (+15%)")
    if pq.window_orientation:
        return (pq.model_copy(update={"window_orientation": []}),
                "снят фильтр ориентации окон")
    if pq.noise_max is not None:
        return (pq.model_copy(update={"noise_max": None}),
                "снят фильтр уровня шума")
    return None


def retrieve_with_relaxation(
        conn, pq: ParsedQuery, *,
        point: PointConstraint | None = None,
        provider: IsochroneProvider | None = None,
        model=None, query_vec=None,
        min_results: int | None = None, max_iters: int | None = None,
        geocoder=None,
        search_fn=hybrid_search) -> tuple[list[Candidate], list[str], ParsedQuery]:
    """Маршрутизация: кастомная точка и/или область (сторона города / место) →
    гео-предикаты, затем retrieval. Мало результатов → ослабляем и повторяем."""
    min_r = min_results if min_results is not None else settings.min_results
    iters = max_iters if max_iters is not None else settings.relaxation_max_iters

    # гео-предикаты: кастомная точка (из запроса API) + область (из NLU).
    # Оба — жёсткие; комбинируются через AND. Резолвим ОДИН раз до петли
    # (геокод дорогой; область при relaxation не меняется).
    preds: list[str] = []
    params: list = []
    if point is not None:
        sql, p = point_predicate(point.lon, point.lat, point.minutes,
                                 provider, point.mode)
        preds.append(sql); params.extend(p)
    if pq.area:
        resolved = (resolve_area(pq.area) if geocoder is None
                    else resolve_area(pq.area, geocoder=geocoder))
        if resolved is not None:
            sql, p = resolved
            preds.append(sql); params.extend(p)
    geo_sql: str | None = " AND ".join(f"({s})" for s in preds) if preds else None
    geo_params: Sequence = params

    relaxed: list[str] = []
    cur_pq = pq
    cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                      geo_sql=geo_sql, geo_params=geo_params)
    for _ in range(iters):
        if len(cands) >= min_r:
            break
        step = relax(cur_pq)
        if step is None:
            break
        cur_pq, note = step
        relaxed.append(note)
        cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                          geo_sql=geo_sql, geo_params=geo_params)
    return cands, relaxed, cur_pq
