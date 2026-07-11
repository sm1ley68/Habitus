# habitus/online/retrieval.py — сердце RAG: WHERE-фильтр + dense + sparse + RRF
from dataclasses import dataclass
from datetime import datetime
from typing import Sequence

import psycopg
from psycopg.rows import dict_row

from habitus.config import settings
from habitus.embed.encode import SPARSE_DIM, encode_texts, to_sparsevec_literal
from habitus.online.schema import ParsedQuery

NOISE_ORDER = ["low", "medium", "high"]

# факты, которые едут в ResultItem.address_facts и в объяснение
FACT_COLUMNS = ("walk_min_school", "walk_min_metro", "walk_min_park",
                "bar_density_500m", "noise_level", "window_orientation")


@dataclass
class Candidate:
    external_id: str
    doc_text: str
    price: int | None
    area: float | None
    rooms: int | None
    facts: dict
    score: float
    updated_at: datetime


def rrf_merge(rankings: Sequence[Sequence[str]], k: int = 60) -> list[tuple[str, float]]:
    """Reciprocal Rank Fusion: score = Σ 1/(k+rank), rank с 1. Тай-брейк по id."""
    scores: dict[str, float] = {}
    for ranking in rankings:
        for rank, ext_id in enumerate(ranking, start=1):
            scores[ext_id] = scores.get(ext_id, 0.0) + 1.0 / (k + rank)
    return sorted(scores.items(), key=lambda kv: (-kv[1], kv[0]))


def build_where(pq: ParsedQuery, extra_sql: str | None = None,
                extra_params: Sequence = ()) -> tuple[str, list]:
    """ParsedQuery → параметризованный WHERE. Порядок клауз фиксирован."""
    clauses: list[str] = ["is_active = TRUE"]
    params: list = []
    if pq.price_min is not None:
        clauses.append("price >= %s"); params.append(pq.price_min)
    if pq.price_max is not None:
        clauses.append("price <= %s"); params.append(pq.price_max)
    if pq.rooms:
        clauses.append("rooms = ANY(%s)"); params.append(list(pq.rooms))
    if pq.area_min is not None:
        clauses.append("area >= %s"); params.append(pq.area_min)
    if pq.area_max is not None:
        clauses.append("area <= %s"); params.append(pq.area_max)
    for g in pq.geo:  # g.kind — Literal["school","metro","park"] → имя колонки безопасно
        clauses.append(f"walk_min_{g.kind} <= %s"); params.append(g.walk_minutes)
    if pq.noise_max is not None and pq.noise_max != "high":
        allowed = NOISE_ORDER[: NOISE_ORDER.index(pq.noise_max) + 1]
        clauses.append("noise_level = ANY(%s)"); params.append(allowed)
    if pq.window_orientation:
        clauses.append("window_orientation && %s"); params.append(list(pq.window_orientation))
    if "bars" in pq.stop_factors:
        clauses.append("bar_density_500m = 0")
    if extra_sql:
        clauses.append(extra_sql); params.extend(extra_params)
    return " AND ".join(clauses), params


def encode_query(text: str, model=None) -> tuple[list[float], dict[int, float]]:
    """Запрос → (dense 1024, sparse-веса) тем же BGE-M3, что и документы."""
    enc = encode_texts([text], model=model)[0]
    return enc["dense"], enc["sparse"]


def _vec_literal(dense: list[float]) -> str:
    return "[" + ",".join(f"{x:g}" for x in dense) + "]"


def _channel_search(conn: psycopg.Connection, sql: str, params: Sequence) -> list[str]:
    """Один векторный канал. Грабля фильтрованного HNSW: при жёстком WHERE индекс
    отдаёт < LIMIT строк — лечится iterative_scan=strict_order (pgvector >= 0.8).
    На старом pgvector GUC нет → savepoint откатывается, идём без него."""
    with conn.transaction():
        try:
            with conn.transaction():  # savepoint: ошибка SET не рушит внешний tx
                conn.execute("SET LOCAL hnsw.iterative_scan = 'strict_order';")
        except psycopg.errors.UndefinedObject:
            pass
        return [r[0] for r in conn.execute(sql, list(params)).fetchall()]


def _fetch_candidates(conn: psycopg.Connection, ext_ids: list[str],
                      scores: dict[str, float]) -> list[Candidate]:
    if not ext_ids:
        return []
    cols = ", ".join(("external_id", "doc_text", "price", "area", "rooms",
                      "updated_at") + FACT_COLUMNS)
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute(f"SELECT {cols} FROM listings WHERE external_id = ANY(%s);",
                    (ext_ids,))
        rows = {r["external_id"]: r for r in cur.fetchall()}
    out = []
    for eid in ext_ids:
        r = rows.get(eid)
        if r is None:
            continue
        out.append(Candidate(
            external_id=eid, doc_text=r["doc_text"] or "", price=r["price"],
            area=r["area"], rooms=r["rooms"],
            facts={c: r[c] for c in FACT_COLUMNS},
            score=scores.get(eid, 0.0), updated_at=r["updated_at"]))
    return out


def filter_only_search(conn: psycopg.Connection, pq: ParsedQuery,
                       top_k: int | None = None, geo_sql: str | None = None,
                       geo_params: Sequence = ()) -> list[Candidate]:
    """Деградация «без вектора»: только SQL-фильтры, свежие сверху."""
    k = top_k or settings.retrieval_top_k
    where, params = build_where(pq, geo_sql, geo_params)
    with conn.cursor() as cur:
        cur.execute(f"SELECT external_id FROM listings WHERE {where} "
                    f"ORDER BY updated_at DESC LIMIT %s;", params + [k])
        ids = [r[0] for r in cur.fetchall()]
    return _fetch_candidates(conn, ids, {})


def hybrid_search(conn: psycopg.Connection, pq: ParsedQuery, *, model=None,
                  top_k: int | None = None, geo_sql: str | None = None,
                  geo_params: Sequence = (),
                  query_vec: tuple[list[float], dict[int, float]] | None = None,
                  channels: tuple[str, ...] = ("dense", "sparse")) -> list[Candidate]:
    """WHERE + dense + sparse → RRF → top-K кандидатов (порядок RRF)."""
    k = top_k or settings.retrieval_top_k
    if query_vec is None:
        if not pq.semantic_text:
            return filter_only_search(conn, pq, k, geo_sql, geo_params)
        query_vec = encode_query(pq.semantic_text, model=model)
    qdense, qsparse = query_vec

    where, params = build_where(pq, geo_sql, geo_params)
    rankings: list[list[str]] = []
    if "dense" in channels:
        rankings.append(_channel_search(
            conn,
            f"SELECT external_id FROM listings WHERE {where} "
            f"AND embedding IS NOT NULL ORDER BY embedding <=> %s::vector LIMIT %s;",
            params + [_vec_literal(qdense), k]))
    # watch-item B: пустой sparse-вектор ({}/dim) даёт cosine-расстояние NaN на
    # нулевом векторе → неопределённый порядок. Канал запускаем только когда
    # есть реальные sparse-веса.
    if "sparse" in channels and qsparse:
        rankings.append(_channel_search(
            conn,
            f"SELECT external_id FROM listings WHERE {where} "
            f"AND sparse_embedding IS NOT NULL "
            f"ORDER BY sparse_embedding <=> %s::sparsevec LIMIT %s;",
            params + [to_sparsevec_literal(qsparse, SPARSE_DIM), k]))

    merged = rrf_merge(rankings, k=settings.rrf_k)[:k]
    ids = [eid for eid, _ in merged]
    return _fetch_candidates(conn, ids, dict(merged))
