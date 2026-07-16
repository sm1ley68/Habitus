# habitus/online/service.py — тонкий FastAPI: валидация входа + вызов pipeline.
# Gateway/деплой — зона беков; бизнес-логики здесь нет.
import asyncio

from fastapi import FastAPI, HTTPException, Request

from habitus.config import settings
from habitus.db.connection import get_conn
from habitus.online.pipeline import run_search
from habitus.online.dossier import DossierNotFound, build_dossier
from habitus.online.object_qa import answer_object_async
from habitus.online.schema import (DossierRequest, DossierResponse,
                                   ObjectAskRequest, ObjectAskResponse,
                                   SearchRequest, SearchResponse)

app = FastAPI(title="Habitus Search")


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
                          provider=provider)


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
