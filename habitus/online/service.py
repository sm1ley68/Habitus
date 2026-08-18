# habitus/online/service.py — тонкий FastAPI: валидация входа + вызов pipeline.
# Gateway/деплой — зона беков; бизнес-логики здесь нет.
import asyncio
import json
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import StreamingResponse

from habitus.config import settings
from habitus.db.connection import get_conn
from habitus.db.init_db import init_db
from habitus.online.cache import explain_cache
from habitus.online.explain import cache_key as explain_cache_key
from habitus.online.explain import explain_stream
from habitus.online.pipeline import run_search
from habitus.online.dossier import DossierNotFound, build_dossier
from habitus.online.object_qa import answer_object_async
from habitus.online.schema import (DossierRequest, DossierResponse,
                                   ExplainRequest, ObjectAskRequest,
                                   ObjectAskResponse, SearchRequest,
                                   SearchResponse)


@asynccontextmanager
async def lifespan(app: FastAPI):
    # /dossier читает urban_evidence/urban_features. Без этих таблиц запрос
    # падает с UndefinedTable (HTTP 500) вместо честной деградации до secondary.
    # init_db идемпотентен (CREATE TABLE IF NOT EXISTS) и повторяет CLI — так
    # схема существует ещё до первого import-evidence/import-osm-features.
    with get_conn() as conn:
        init_db(conn)
    yield


app = FastAPI(title="Habitus Search", lifespan=lifespan)


@app.get("/health")
def health() -> dict:
    return {"status": "ok"}


@app.post("/search", response_model=SearchResponse)
def search(req: SearchRequest) -> SearchResponse:
    llm = None
    if settings.openrouter_api_key:
        from habitus.online.llm import OpenRouterLLM
        llm = OpenRouterLLM()
    provider = None
    if settings.ors_api_key:
        from habitus.online.geo import ORSProvider
        provider = ORSProvider()
    with get_conn() as conn:
        return run_search(req.query, conn, llm=llm, point=req.point,
                          provider=provider, city=req.city, explain=req.explain)


def _sse(event: str, data: dict) -> str:
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n"


@app.post("/explain/stream")
async def explain_stream_endpoint(req: ExplainRequest) -> StreamingResponse:
    """Объяснение поверх уже готовой выдачи, токенами по мере генерации.

    Отдельный вызов, а не часть /search: шлюз показывает объекты сразу, а текст
    доливает в тот же SSE-поток по мере готовности.
    """
    key = explain_cache_key(req.query, req.results)
    cached = explain_cache.get(key)

    async def frames() -> AsyncIterator[str]:
        if cached is not None:
            yield _sse("token", {"token": cached})
            yield _sse("done", {"llm_ok": True})
            return

        llm = None
        if settings.openrouter_api_key:
            from habitus.online.llm import AsyncOpenRouterLLM
            llm = AsyncOpenRouterLLM()

        parts: list[str] = []
        async for event in explain_stream(req.query, req.results, req.relaxed, llm):
            if "token" in event:
                parts.append(event["token"])
                yield _sse("token", {"token": event["token"]})
            else:
                # шаблон и оборванный поток не кэшируем: деградация не должна
                # навсегда подменить объяснение для этого запроса
                if event["llm_ok"]:
                    explain_cache.put(key, "".join(parts))
                yield _sse("done", {"llm_ok": event["llm_ok"]})

    return StreamingResponse(frames(), media_type="text/event-stream",
                             headers={"Cache-Control": "no-cache",
                                      "X-Accel-Buffering": "no"})


@app.post("/dossier", response_model=DossierResponse)
def dossier(req: DossierRequest) -> DossierResponse:
    provider = None
    if settings.ors_api_key:
        from habitus.online.geo import ORSProvider
        provider = ORSProvider()
    try:
        with get_conn() as conn:
            payload = build_dossier(req, conn, route_provider=provider)
    except DossierNotFound as exc:
        raise HTTPException(status_code=404, detail="object not found") from exc
    return DossierResponse(dossier=payload)


@app.post("/object-ask", response_model=ObjectAskResponse)
async def object_ask(req: ObjectAskRequest, request: Request) -> ObjectAskResponse:
    llm = None
    if settings.openrouter_api_key:
        from habitus.online.llm import AsyncOpenRouterLLM
        llm = AsyncOpenRouterLLM()
    task = asyncio.create_task(answer_object_async(req, llm))
    try:
        while not task.done():
            if await request.is_disconnected():
                task.cancel()
                raise HTTPException(status_code=499, detail="client disconnected")
            await asyncio.sleep(.1)
        return await task
    finally:
        if not task.done():
            task.cancel()
