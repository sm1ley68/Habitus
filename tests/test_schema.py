import psycopg
from habitus.db.init_db import ensure_sparse_index, init_db
from habitus.config import settings

def test_init_db_creates_tables():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT to_regclass('listings'), to_regclass('poi');")
            listings, poi = cur.fetchone()
        assert listings == "listings"
        assert poi == "poi"

def test_sparse_channel_is_indexed():
    # Без этого индекса sparse-канал hybrid_search сканирует всю таблицу
    # на каждый поиск — половина RRF работает вхолостую.
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT indexdef FROM pg_indexes "
                        "WHERE indexname='listings_sparse_hnsw';")
            row = cur.fetchone()
        assert row is not None, "HNSW по sparse_embedding не создан"
        assert "hnsw" in row[0].lower()
        assert "sparsevec_cosine_ops" in row[0]


def test_unindexable_row_does_not_break_startup():
    # Строка с >1000 ненулевых могла попасть в БД до появления SPARSE_MAX_NNZ.
    # pgvector откажется её индексировать — но init_db обязан пережить отказ,
    # иначе ML-сервис не поднимется вообще.
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("DROP INDEX IF EXISTS listings_sparse_hnsw;")
            cur.execute("TRUNCATE listings;")
            fat = "{" + ",".join(f"{i}:1" for i in range(1, 1202)) + "}/250002"
            cur.execute("INSERT INTO listings (external_id, source, sparse_embedding)"
                        " VALUES ('FAT','test',%s::sparsevec);", (fat,))
        conn.commit()

        assert ensure_sparse_index(conn) is False
        assert conn.execute("SELECT 1;").fetchone() == (1,)  # соединение живо

        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
        conn.commit()
        assert ensure_sparse_index(conn) is True


def test_extensions_present():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("SELECT extname FROM pg_extension;")
            names = {r[0] for r in cur.fetchall()}
        assert "postgis" in names
        assert "vector" in names


def test_enrichment_columns_exist():
    expected = {
        ("listings", "city"), ("listings", "address"),
        ("listings", "source_url"), ("listings", "metro_station"),
        ("listings", "walk_min_metro_src"), ("listings", "source_extra"),
        ("poi", "city"),
        ("raw_listings", "city"), ("raw_listings", "address"),
        ("raw_listings", "source_url"), ("raw_listings", "source_extra"),
    }
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("""SELECT table_name, column_name FROM information_schema.columns
                           WHERE table_schema='public'
                             AND table_name IN ('listings','poi','raw_listings');""")
            got = {(t, c) for t, c in cur.fetchall()}
    assert expected <= got, f"нет колонок: {expected - got}"


def test_existing_rows_default_to_moscow():
    # DEFAULT 'msk' делает backfill бесплатным: всё, что уже в базе, — московское
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("INSERT INTO listings (external_id, source) VALUES ('C1','test');")
            cur.execute("SELECT city, source_extra FROM listings WHERE external_id='C1';")
            city, extra = cur.fetchone()
    assert city == "msk"
    assert extra == {}
