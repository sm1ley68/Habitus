# habitus/online/orchestrator.py — маршрутизация + relaxation loop
from typing import Sequence

from habitus.config import settings
from habitus.online.geo import AreaMatch, IsochroneProvider, point_predicate
from habitus.online.metro_route import metro_predicate_with_note
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
        city: str | None = None,
        household: Sequence[tuple[float, float]] = (),
        search_fn=hybrid_search) -> tuple[list[Candidate], list[str], ParsedQuery]:
    """Маршрутизация: кастомная точка (из запроса API) + готовая область
    (`AreaMatch`, резолвится заранее в pipeline) → гео-предикаты, затем
    retrieval. Мало результатов → сперва штатный relax (гео/цена/шум; ориентация
    окон — не фильтр, ослаблять нечего), затем — если область была задана —
    авто-расширение по AreaMatch.widen."""
    min_r = min_results if min_results is not None else settings.min_results
    iters = max_iters if max_iters is not None else settings.relaxation_max_iters

    # R70 (фикс-раунд 1): объявлен здесь, а не ниже, чтобы блок metro мог
    # положить в него заметку о снятом фильтре ДО начала цикла ослабления —
    # pipeline.py сознательно не пускает point-предикаты в
    # constraint_diagnostics (комментарий у 4.5), так что relaxed — ЕДИНСТВЕННЫЙ
    # канал, которым пользователь вообще может узнать, что его «метро ≤N мин»
    # тихо не применился.
    relaxed: list[str] = []
    base_sql, base_params = None, []
    if point is not None:
        if point.mode == "metro":
            # Метро считает внутренний движок по графу: изохроны ORS для
            # public transport непригодны (см. ORSProvider.directions).
            # Графа для города нет, либо у точки нет платформ в зоне охвата
            # (3-км потолок nearest_stations, R68) → ограничение не
            # накладывается вовсе: молча обнулять выдачу нельзя, а врать
            # оценкой — тем более. metro_predicate НЕ попадает в
            # constraint_diagnostics (pipeline.py исключает point-предикаты
            # из диагностики намеренно) — единственный способ дать
            # пользователю знать об этом деградационном пути, это заметка в
            # relaxed (R70): она доходит до текста объяснения через
            # build_explanation(notes=...).
            # R90/R91 (сквозное ревью ветки): причин деградации теперь
            # четыре (нет графа; у точки нет платформ; по городу не
            # рассчитаны пешие плечи; граф города разорван), и заметка
            # приходит ГОТОВОЙ из metro_predicate_with_note — здесь её
            # нельзя переформулировать общей фразой, потому что «графа нет»
            # и «граф есть, но разорван» — разные факты о городе, а писать
            # неверный из них пользователю запрещено ровно так же, как
            # выдумывать числа. Заметка приходит и при ПРИМЕНЁННОМ фильтре:
            # разорванный граф в пределах допуска оставляет часть города
            # неоценённой, и молчать об этом тоже нельзя.
            got, note = metro_predicate_with_note(
                conn, city or "msk", point.lon, point.lat, point.minutes)
            if got is not None:
                base_sql, base_params = got[0], list(got[1])
            if note:
                relaxed.append(note)
        else:
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

    cur_pq = pq
    gsql, gpar = geo()
    cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                      geo_sql=gsql, geo_params=gpar, city=city,
                      household=household)
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
                          geo_sql=gsql, geo_params=gpar, city=city,
                          household=household)
    # авто-расширение области, если всё ещё мало
    while len(cands) < min_r and area_steps:
        wsql, wpar, wlabel = area_steps.pop(0)
        area_sql = None if wsql == "TRUE" else wsql
        area_params = [] if wsql == "TRUE" else list(wpar)
        relaxed.append(wlabel)
        gsql, gpar = geo()
        cands = search_fn(conn, cur_pq, model=model, query_vec=query_vec,
                          geo_sql=gsql, geo_params=gpar, city=city,
                          household=household)
    return cands, relaxed, cur_pq
