# habitus/online/orchestrator.py — маршрутизация + relaxation loop
from habitus.config import settings
from habitus.online.geo import AreaMatch, IsochroneProvider, point_predicate
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
        area_match: AreaMatch | None = None,
        search_fn=hybrid_search) -> tuple[list[Candidate], list[str], ParsedQuery]:
    """Маршрутизация: кастомная точка (из запроса API) + готовая область
    (`AreaMatch`, резолвится заранее в pipeline) → гео-предикаты, затем
    retrieval. Мало результатов → сперва штатный relax (гео/цена/ориентация/
    шум), затем — если область была задана — авто-расширение по AreaMatch.widen."""
    min_r = min_results if min_results is not None else settings.min_results
    iters = max_iters if max_iters is not None else settings.relaxation_max_iters

    base_sql, base_params = None, []
    if point is not None:
        s, p = point_predicate(point.lon, point.lat, point.minutes,
                               provider, point.mode)
        base_sql, base_params = s, list(p)

    area_sql = area_match.sql if area_match else None
    area_params = list(area_match.params) if area_match else []
    area_steps = list(area_match.widen) if area_match else []

    def geo():
        parts = ([base_sql] if base_sql else []) + ([area_sql] if area_sql else [])
        sql = " AND ".join(f"({x})" for x in parts) if parts else None
        return sql, base_params + area_params

    relaxed: list[str] = []
    cur_pq = pq
    gsql, gpar = geo()
    cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                      geo_sql=gsql, geo_params=gpar)
    for _ in range(iters):
        if len(cands) >= min_r:
            break
        step = relax(cur_pq)
        if step is None:
            break
        cur_pq, note = step
        relaxed.append(note)
        gsql, gpar = geo()
        cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                          geo_sql=gsql, geo_params=gpar)
    # авто-расширение области, если всё ещё мало
    while len(cands) < min_r and area_steps:
        wsql, wpar, wlabel = area_steps.pop(0)
        area_sql = None if wsql == "TRUE" else wsql
        area_params = [] if wsql == "TRUE" else list(wpar)
        relaxed.append(wlabel)
        gsql, gpar = geo()
        cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                          geo_sql=gsql, geo_params=gpar)
    return cands, relaxed, cur_pq
