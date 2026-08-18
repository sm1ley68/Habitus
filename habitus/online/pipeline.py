# habitus/online/pipeline.py — сборка end-to-end + деградация по слоям
import logging

from habitus.config import settings
from habitus.online import trace
from habitus.online.cache import embed_cache, explain_cache, parse_cache
from habitus.online.explain import cache_key as explain_cache_key
from habitus.online.explain import explain as build_explanation
from habitus.online.geo import IsochroneProvider
from habitus.online.llm import LLMClient, LLMUnavailable
from habitus.online.nlu import ParseError, merge_parsed, parse_turn
from habitus.online.orchestrator import retrieve_with_relaxation
from habitus.online.rerank import prefilter_pool, proximity_rerank, rerank
from habitus.online.retrieval import Candidate, encode_query
from habitus.online.schema import (ParsedQuery, PointConstraint, ResultItem,
                                   SearchResponse, TurnIntent)

log = logging.getLogger("habitus.online.pipeline")


def to_result_item(c: Candidate) -> ResultItem:
    return ResultItem(external_id=c.external_id, price=c.price, area=c.area,
                      rooms=c.rooms, address_facts=c.facts, score=c.score)


@trace.with_timings
def run_search(query: str, conn, *, llm: LLMClient | None = None,
               point: PointConstraint | None = None,
               provider: IsochroneProvider | None = None,
               model=None, reranker=None,
               min_results: int | None = None,
               city: str = "msk", explain: bool = True,
               prev_parsed: ParsedQuery | None = None) -> SearchResponse:
    degraded: list[str] = []

    # 1. NLU: parse_turn классифицирует намерение реплики и (при prev_parsed)
    #    разбирает её относительно предыдущего шага диалога; merge_parsed
    #    накладывает результат на prev_parsed. Кэш по хэшу текста работает
    #    только для первого шага диалога (prev_parsed=None) — ответ на ту же
    #    реплику с другим prev_parsed был бы другим, кэшировать его по одному
    #    только тексту нельзя.
    intent: TurnIntent = "new_search"
    pq: ParsedQuery | None = parse_cache.get(query) if prev_parsed is None else None
    if pq is None:
        if llm is None:
            pq = ParsedQuery(semantic_text=query)
            degraded.append("nlu")
        else:
            try:
                with trace.span("parse"):
                    turn = parse_turn(query, llm, prev_parsed)
                pq = merge_parsed(prev_parsed or ParsedQuery(), turn)
                intent = turn.intent
                if prev_parsed is None:
                    parse_cache.put(query, pq)
            except (ParseError, LLMUnavailable):
                pq = ParsedQuery(semantic_text=query)
                degraded.append("nlu")

    # 2. кодирование запроса (кэш; отказ → filter-only retrieval)
    query_vec = None
    search_pq = pq
    if pq.semantic_text:
        query_vec = embed_cache.get(pq.semantic_text)
        if query_vec is None:
            try:
                with trace.span("encode"):
                    query_vec = encode_query(pq.semantic_text, model=model)
                embed_cache.put(pq.semantic_text, query_vec)
            except Exception as exc:
                log.warning("деградация слоя encode-вектора запроса: %s",
                           exc, exc_info=True)
                degraded.append("vector")
                search_pq = pq.model_copy(update={"semantic_text": ""})

    # 2.5 резолв области поиска (район/сторона города/именованное место) →
    #     готовый AreaMatch для оркестратора; отказ резолвера деградирует
    #     без 500 — retrieval просто идёт без гео-фильтра по области.
    from habitus.online.geo import resolve_area
    area_match = None
    if pq.area:
        try:
            with trace.span("resolve_area"):
                area_match = resolve_area(pq.area, conn, city=city)
        except Exception as exc:
            log.warning("резолв области не удался: %s", exc, exc_info=True)
    area_label = area_match.label if area_match else None

    # 3. retrieval + relaxation
    with trace.span("retrieval"):
        cands, relaxed, _ = retrieve_with_relaxation(
            conn, search_pq, point=point, provider=provider,
            model=model, query_vec=query_vec, area_match=area_match,
            min_results=min_results, city=city)

    # 4. пул сужен до rerank_pool_n ДО кросс-энкодера (реранк линеен по числу
    #    пар и составляет львиную долю латентности — settings.rerank_pool_n),
    #    затем proximity-бленд точной близости поверх скоров, срез top-N
    #    (кросс-энкодер слеп к точным минутам walk_min_* — их добавляет
    #    proximity-стадия). Отказ реранкера деградирует именно суженный пул,
    #    а не весь cands — дальше по пайплайну идёт то же множество кандидатов.
    pool = prefilter_pool(pq, cands)
    try:
        with trace.span("rerank", n=len(pool)):
            ranked = rerank(query, pool, top_n=len(pool), reranker=reranker)
    except Exception as exc:
        log.warning("деградация слоя reranker: %s", exc, exc_info=True)
        degraded.append("reranker")
        ranked = pool
    top = proximity_rerank(pq, ranked, top_n=settings.rerank_top_n)

    results = [to_result_item(c) for c in top]

    # 5. объяснение (кэш по запросу+выдаче; отказ → шаблон).
    #    explain=False — шлюз забирает текст отдельным потоковым вызовом
    #    /explain/stream и показывает его по мере генерации; держать выдачу
    #    объектов ещё 6–17 с ради того же текста незачем.
    explanation = ""
    if explain:
        exp_key = explain_cache_key(query, results)
        explanation = explain_cache.get(exp_key)
        if explanation is None:
            with trace.span("explain"):
                explanation, llm_ok = build_explanation(query, results, relaxed, llm)
            if llm_ok:
                explain_cache.put(exp_key, explanation)
            else:
                degraded.append("llm")

    freshness = max((c.updated_at for c in top), default=None)
    data_freshness = (f"данные актуальны на {freshness:%Y-%m-%d %H:%M}"
                      if freshness else "нет данных")

    area_geo = None
    if area_match is not None:
        try:
            from habitus.online.geo import area_geojson
            area_geo = area_geojson(area_match, conn)
        except Exception as exc:
            log.warning("сбор геометрии зоны не удался: %s", exc, exc_info=True)

    return SearchResponse(results=results, explanation=explanation, parsed=pq,
                          relaxed=relaxed, data_freshness=data_freshness,
                          degraded=degraded, area_label=area_label,
                          area_geojson=area_geo, intent=intent)
