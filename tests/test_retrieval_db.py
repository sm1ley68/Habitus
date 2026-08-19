import psycopg
import pytest
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import SPARSE_DIM, to_sparsevec_literal
from habitus.online.retrieval import (filter_only_search, hybrid_search,
                                      orientation_coverage)
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


def test_facts_carry_address_and_station(conn):
    with conn.cursor() as cur:
        cur.execute("UPDATE listings SET address=%s, metro_station=%s "
                    "WHERE external_id='A';",
                    ("Москва, Хамовники, Комсомольский проспект", "Парк культуры"))
    conn.commit()
    cands = filter_only_search(conn, ParsedQuery())
    a = next(c for c in cands if c.external_id == "A")
    assert a.facts["address"] == "Москва, Хамовники, Комсомольский проспект"
    assert a.facts["metro_station"] == "Парк культуры"


@pytest.fixture
def coverage_conn():
    """Свой срез под замер покрытия: ориентация есть не у всех, плюс строки,
    которые в срез попадать не должны (неактивная и другой город)."""
    rows = [
        ("CA", True, "msk", ["SW"]),
        ("CB", True, "msk", ["N", "E"]),
        ("CC", True, "msk", None),        # данных нет
        ("CD", True, "msk", []),          # пустой массив — тоже «данных нет»
        ("CE", False, "msk", ["SW"]),     # неактивна — вне среза
        ("CF", True, "spb", ["SW"]),      # другой город — вне среза
    ]
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for eid, active, city, orient in rows:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, city,
                           price, rooms, area, window_orientation, doc_text)
                       VALUES (%s,'test',%s,%s,10000000,2,50,%s,%s);""",
                    (eid, active, city, orient, f"объект {eid}"))
        c.commit()
        yield c


def test_orientation_coverage_counts_only_rows_with_data(coverage_conn):
    # Порядок значений в кортеже важен: перевёрнутый дал бы «4 из 2».
    with_data, total = orientation_coverage(coverage_conn, "msk")
    assert (with_data, total) == (2, 4)


def test_orientation_coverage_treats_empty_array_as_no_data(coverage_conn):
    # Пустой массив — то же «данных нет», что NULL (так же считает clean/windows.py):
    # бонуса в proximity_rerank такая строка не получит, значит и в честную
    # цифру покрытия попадать не должна.
    with coverage_conn.cursor() as cur:
        cur.execute("UPDATE listings SET window_orientation='{}' "
                    "WHERE external_id IN ('CA','CB');")
    coverage_conn.commit()
    with_data, _ = orientation_coverage(coverage_conn, "msk")
    assert with_data == 0


def test_orientation_coverage_empty_city_slice_reports_zero_total(coverage_conn):
    # Города без объявлений: total=0 — пайплайн на этом обязан не сочинять
    # процент, а вообще не добавлять заметку.
    with_data, total = orientation_coverage(coverage_conn, "spb")
    assert with_data == 1 and total == 1
    with coverage_conn.cursor() as cur:
        cur.execute("DELETE FROM listings WHERE city='spb';")
    coverage_conn.commit()
    assert orientation_coverage(coverage_conn, "spb") == (0, 0)
