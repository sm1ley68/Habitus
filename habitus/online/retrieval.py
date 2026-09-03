# habitus/online/retrieval.py — сердце RAG: WHERE-фильтр + dense + sparse + RRF
from dataclasses import dataclass
from datetime import datetime
from itertools import groupby
from typing import Sequence

import psycopg
from psycopg.rows import dict_row

from habitus.config import settings
from habitus.embed.encode import SPARSE_DIM, encode_texts, to_sparsevec_literal
from habitus.online.schema import ParsedQuery

NOISE_ORDER = ["low", "medium", "high"]

# факты, которые едут в ResultItem.address_facts и в объяснение
FACT_COLUMNS = ("walk_min_school", "walk_min_metro", "walk_min_park",
                "bar_density_500m", "noise_level", "window_orientation",
                "address", "metro_station")


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
    # Координаты объекта — отдельными полями, а НЕ в facts: facts целиком
    # уезжают в ResultItem.address_facts и оттуда в промпт объяснения, где
    # сырые градусы ничего не объясняют и только жрут контекст. Нужны
    # ранжированию по точкам домохозяйства (rerank._household_norm).
    # None — у объявления нет geom; такой кандидат в сигнале не участвует.
    lon: float | None = None
    lat: float | None = None


def rrf_merge(rankings: Sequence[Sequence[str]], k: int = 60) -> list[tuple[str, float]]:
    """Reciprocal Rank Fusion: score = Σ 1/(k+rank), rank с 1. Тай-брейк по id."""
    scores: dict[str, float] = {}
    for ranking in rankings:
        for rank, ext_id in enumerate(ranking, start=1):
            scores[ext_id] = scores.get(ext_id, 0.0) + 1.0 / (k + rank)
    return sorted(scores.items(), key=lambda kv: (-kv[1], kv[0]))


def _where_groups(pq: ParsedQuery, extra_sql: str | None = None,
                  extra_params: Sequence = (),
                  city: str | None = None) -> list[tuple[str, str, list]]:
    """Клаузы build_where как (человекочитаемая метка, SQL, params), в порядке
    наложения. Общий источник для build_where и constraint_diagnostics — чтобы
    диагностика не могла разойтись с реальным фильтром retrieval."""
    groups: list[tuple[str, str, list]] = [("база", "is_active = TRUE", [])]
    if city:  # самый селективный фильтр — первым
        groups.append(("база", "city = %s", [city]))
    if pq.price_min is not None:
        groups.append(("цена", "price >= %s", [pq.price_min]))
    if pq.price_max is not None:
        groups.append(("цена", "price <= %s", [pq.price_max]))
    if pq.rooms:
        groups.append(("комнаты", "rooms = ANY(%s)", [list(pq.rooms)]))
    if pq.area_min is not None:
        groups.append(("площадь", "area >= %s", [pq.area_min]))
    if pq.area_max is not None:
        groups.append(("площадь", "area <= %s", [pq.area_max]))
    for g in pq.geo:  # g.kind — Literal["school","metro","park"] → имя колонки безопасно
        groups.append(("гео-минуты", f"walk_min_{g.kind} <= %s", [g.walk_minutes]))
    if pq.noise_max is not None and pq.noise_max != "high":
        allowed = NOISE_ORDER[: NOISE_ORDER.index(pq.noise_max) + 1]
        groups.append(("шум", "noise_level = ANY(%s)", [allowed]))
    # window_orientation НЕ фильтр: заполнен у ~2% объявлений (см.
    # settings.orientation_weight), жёсткая клауза отсекала бы базу целиком.
    # Ориентация учитывается как мягкий сигнал в proximity_rerank.
    if "bars" in pq.stop_factors:
        groups.append(("стоп-факторы", "bar_density_500m = 0", []))
    if extra_sql:
        groups.append(("гео-предикат области", extra_sql, list(extra_params)))
    return groups


def build_where(pq: ParsedQuery, extra_sql: str | None = None,
                extra_params: Sequence = (),
                city: str | None = None) -> tuple[str, list]:
    """ParsedQuery → параметризованный WHERE. Порядок клауз фиксирован."""
    groups = _where_groups(pq, extra_sql, extra_params, city)
    clauses = [clause for _, clause, _ in groups]
    params: list = []
    for _, _, cparams in groups:
        params.extend(cparams)
    return " AND ".join(clauses), params


def constraint_diagnostics(conn: psycopg.Connection, pq: ParsedQuery,
                           geo_sql: str | None = None,
                           geo_params: Sequence = (),
                           city: str | None = None) -> list[dict]:
    """Сколько объектов остаётся при последовательном наложении клауз
    build_where — чтобы на пустой выдаче было видно, какое именно условие её
    обнулило. Группы и их порядок берутся из _where_groups (общий источник с
    build_where), соседние клаузы с одной меткой (например price_min+price_max)
    схлопываются в один шаг. Только COUNT(*), параметры — исключительно через
    плейсхолдеры %s, без склейки строк."""
    groups = _where_groups(pq, geo_sql, geo_params, city)
    # подряд идущие клаузы с одной меткой (price_min+price_max, area_min+
    # area_max, несколько гео-минут) — один шаг: пользователю нужна «цена»,
    # а не два одинаково названных шага
    steps = [(label, [c for _, c, _ in grp], [p for _, _, ps in grp for p in ps])
             for label, grp in ((lbl, list(g)) for lbl, g
                                in groupby(groups, key=lambda g: g[0]))]

    out: list[dict] = []
    acc_clauses: list[str] = []
    acc_params: list = []
    with conn.cursor() as cur:
        for label, clauses, cparams in steps:
            acc_clauses.extend(clauses)
            acc_params.extend(cparams)
            where = " AND ".join(acc_clauses)
            cur.execute(f"SELECT count(*) FROM listings WHERE {where};", acc_params)
            out.append({"constraint": label, "remaining": int(cur.fetchone()[0])})
    return out


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


def household_order_sql(points: Sequence[tuple[float, float]]) -> tuple[str, list]:
    """SQL-выражение household_cost (среднее плечо + худшее) и его параметры.

    Зеркало habitus/online/household.household_cost, но считаемое СУБД: канал
    ранжирования должен упорядочить всю отфильтрованную выборку, а не те 100
    кандидатов, которые уже нашла семантика. Метрика обязана совпадать с
    питоновской — иначе retrieval и реранк спорили бы друг с другом о том,
    какое расположение дешевле.

    Координаты идут параметрами, а не склейкой в текст запроса.
    """
    dist = ("ST_Distance(geom::geography, "
            "ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography)")
    dists = [dist] * len(points)
    coords = [c for p in points for c in p]
    mean = "(" + " + ".join(dists) + f") / {len(points)}.0"
    if len(points) == 1:
        return f"({mean} + {dist})", coords + coords
    worst = f"GREATEST({', '.join(dists)})"
    return f"({mean} + {worst})", coords + coords


def _fetch_candidates(conn: psycopg.Connection, ext_ids: list[str],
                      scores: dict[str, float]) -> list[Candidate]:
    if not ext_ids:
        return []
    cols = ", ".join(("external_id", "doc_text", "price", "area", "rooms",
                      "updated_at") + FACT_COLUMNS)
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute(f"SELECT {cols}, ST_X(geom) AS lon, ST_Y(geom) AS lat "
                    f"FROM listings WHERE external_id = ANY(%s);",
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
            score=scores.get(eid, 0.0), updated_at=r["updated_at"],
            lon=r["lon"], lat=r["lat"]))
    return out


def orientation_coverage(conn: psycopg.Connection, city: str | None) -> tuple[int, int]:
    """(сколько активных объявлений города с непустым window_orientation, всего
    активных). Тот же срез, что видит retrieval (build_where(city=...)), — чтобы
    честная цифра покрытия в notes соответствовала реальной базе запроса."""
    where, params = build_where(ParsedQuery(), city=city)
    with conn.cursor() as cur:
        cur.execute(
            # cardinality > 0, а не IS NOT NULL: пустой массив — то же «данных
            # нет», что и NULL (так же считает habitus/clean/windows.py), и
            # бонуса в _orientation_bonus он никогда не получит.
            f"SELECT count(*) FILTER (WHERE cardinality(window_orientation) > 0), "
            f"count(*) FROM listings WHERE {where};", params)
        with_data, total = cur.fetchone()
    return int(with_data), int(total)


def filter_only_search(conn: psycopg.Connection, pq: ParsedQuery,
                       top_k: int | None = None, geo_sql: str | None = None,
                       geo_params: Sequence = (),
                       city: str | None = None,
                       household: Sequence[tuple[float, float]] = ()) -> list[Candidate]:
    """Деградация «без вектора»: только SQL-фильтры. Свежие сверху, а при
    названной семье — сперва те, кому расположение обходится дешевле:
    география семьи известна и без эмбеддингов, терять её на деградационном
    пути незачем."""
    k = top_k or settings.retrieval_top_k
    where, params = build_where(pq, geo_sql, geo_params, city)
    order, order_params = "updated_at DESC", []
    if household:
        order, order_params = household_order_sql(household)
    with conn.cursor() as cur:
        cur.execute(f"SELECT external_id FROM listings WHERE {where} "
                    f"{'AND geom IS NOT NULL ' if household else ''}"
                    f"ORDER BY {order} LIMIT %s;",
                    params + order_params + [k])
        ids = [r[0] for r in cur.fetchall()]
    return _fetch_candidates(conn, ids, {})


def hybrid_search(conn: psycopg.Connection, pq: ParsedQuery, *, model=None,
                  top_k: int | None = None, geo_sql: str | None = None,
                  geo_params: Sequence = (),
                  query_vec: tuple[list[float], dict[int, float]] | None = None,
                  channels: tuple[str, ...] = ("dense", "sparse"),
                  city: str | None = None,
                  household: Sequence[tuple[float, float]] = ()) -> list[Candidate]:
    """WHERE + dense + sparse (+ household) → RRF → top-K кандидатов.

    household — точки, которые назвала семья. Это ТРЕТИЙ канал RRF, а не
    фильтр: он приносит в пул объекты, удачно расположенные относительно
    работы и школы, которых семантика не находит вовсе. Замер показал, зачем:
    до появления канала из десяти эталонных объектов d-серии до реранка
    доезжало 0–5, а у запроса «компромисс Сколково ↔ Сити» — ноль, поэтому
    вес household в реранке ничего изменить не мог: переупорядочить можно
    только то, что уже нашли.

    Каналом, а не клаузой WHERE — сознательно: жёсткий радиус вокруг мест
    семьи обнулял бы выдачу там, где подходящего жилья рядом просто нет,
    вместо того чтобы честно показать компромисс.
    """
    k = top_k or settings.retrieval_top_k
    if query_vec is None:
        if not pq.semantic_text:
            return filter_only_search(conn, pq, k, geo_sql, geo_params, city,
                                      household=household)
        query_vec = encode_query(pq.semantic_text, model=model)
    qdense, qsparse = query_vec

    where, params = build_where(pq, geo_sql, geo_params, city)
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

    if household:
        order_sql, order_params = household_order_sql(household)
        rankings.append(_channel_search(
            conn,
            f"SELECT external_id FROM listings WHERE {where} "
            f"AND geom IS NOT NULL ORDER BY {order_sql} LIMIT %s;",
            params + order_params + [k]))

    merged = rrf_merge(rankings, k=settings.rrf_k)[:k]
    ids = [eid for eid, _ in merged]
    return _fetch_candidates(conn, ids, dict(merged))
