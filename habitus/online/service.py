# habitus/online/service.py — тонкий FastAPI: валидация входа + вызов pipeline.
# Gateway/деплой — зона беков; бизнес-логики здесь нет.
from fastapi import FastAPI

from habitus.config import settings
from habitus.db.connection import get_conn
from habitus.online.pipeline import run_search
from habitus.online.schema import SearchRequest, SearchResponse

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
