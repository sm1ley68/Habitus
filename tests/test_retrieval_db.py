import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import SPARSE_DIM, to_sparsevec_literal
from habitus.online.retrieval import filter_only_search, hybrid_search
from habitus.online.schema import GeoConstraint, ParsedQuery

DIM = 1024


def _axis(i: int) -> list[float]:
    v = [0.0] * DIM
    v[i] = 1.0
    return v


def _vec(v: list[float]) -> str:
    return "[" + ",".join(f"{x:g}" for x in v) + "]"


ROWS = [
    # (eid, price, rooms, walk_school, bars, noise, dense_axis, sparse)
    ("A", 10_000_000, 2, 8.0, 0, "low", 0, {10: 1.0}),
    ("B", 12_000_000, 2, 9.0, 0, "low", 1, {20: 1.0}),
    ("C", 30_000_000, 3, 25.0, 3, "high", 2, {30: 1.0}),
]


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for eid, price, rooms, ws, bars, noise, axis, sparse in ROWS:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, price,
                           rooms, area, walk_min_school, bar_density_500m, noise_level,
                           window_orientation, doc_text, embedding, sparse_embedding)
                       VALUES (%s,'test',TRUE,%s,%s,50,%s,%s,%s,%s,%s,
                               %s::vector,%s::sparsevec);""",
                    (eid, price, rooms, ws, bars, noise, ["SW"],
                     f"объект {eid}", _vec(_axis(axis)),
                     to_sparsevec_literal(sparse, SPARSE_DIM)))
        c.commit()
        yield c


def test_rrf_fuses_dense_and_sparse(conn):
    # dense ближе всех к A (ось 0), sparse матчит B (токен 20)
    cands = hybrid_search(conn, ParsedQuery(semantic_text="x"),
                          query_vec=(_axis(0), {20: 1.0}))
    top2 = {c.external_id for c in cands[:2]}
    assert top2 == {"A", "B"}
    assert all(c.score > 0 for c in cands)
    assert cands[0].facts["noise_level"] in ("low", "high")   # факты доехали


def test_hard_filters_exclude(conn):
    pq = ParsedQuery(price_max=15_000_000, noise_max="low",
                     stop_factors=["bars"],
                     geo=[GeoConstraint(kind="school", walk_minutes=10)],
                     semantic_text="x")
    cands = hybrid_search(conn, pq, query_vec=(_axis(2), {30: 1.0}))
    ids = [c.external_id for c in cands]
    assert "C" not in ids and set(ids) == {"A", "B"}


def test_filtered_hnsw_returns_all_matches(conn):
    # грабля: жёсткий WHERE + HNSW без strict_order отдаёт < LIMIT.
    # Оба подходящих объекта обязаны вернуться.
    cands = hybrid_search(conn, ParsedQuery(price_max=15_000_000, semantic_text="x"),
                          query_vec=(_axis(2), {}), channels=("dense",))
    assert {c.external_id for c in cands} == {"A", "B"}


def test_dense_only_channel(conn):
    cands = hybrid_search(conn, ParsedQuery(semantic_text="x"),
                          query_vec=(_axis(1), {}), channels=("dense",))
    assert cands[0].external_id == "B"


def test_filter_only_search(conn):
    cands = filter_only_search(conn, ParsedQuery(rooms=[2]))
    assert {c.external_id for c in cands} == {"A", "B"}


def test_empty_semantic_text_falls_back_to_filters(conn):
    cands = hybrid_search(conn, ParsedQuery(rooms=[3]))
    assert [c.external_id for c in cands] == ["C"]


def test_empty_sparse_vector_skips_sparse_channel_and_does_not_crash(conn):
    # watch-item B: query_vec=(dense, {}) с channels=("dense","sparse") — пустой
    # sparse-вектор не должен ронять поиск NaN-расстоянием на sparsevec <=>.
    # Канал sparse тихо пропускается, результат целиком определяется dense.
    cands = hybrid_search(conn, ParsedQuery(semantic_text="x"),
                          query_vec=(_axis(1), {}), channels=("dense", "sparse"))
    assert cands[0].external_id == "B"
    assert all(c.score > 0 for c in cands)
