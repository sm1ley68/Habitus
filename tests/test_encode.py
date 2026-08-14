import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import (SPARSE_MAX_NNZ, encode_texts, embed_pending,
                                  prune_sparse, to_sparsevec_literal)


def test_to_sparsevec_literal():
    lit = to_sparsevec_literal({5: 0.7, 100: 0.3}, dim=250002)
    assert lit == "{5:0.7,100:0.3}/250002"


def test_prune_sparse_leaves_short_vectors_untouched():
    sparse = {5: 0.7, 100: 0.3}
    assert prune_sparse(sparse) == sparse


def test_prune_sparse_keeps_the_heaviest_weights_in_index_order():
    # 1500 ненулевых, вес растёт с индексом → выживают 1000 старших индексов
    sparse = {i: i / 1000 for i in range(1, 1501)}
    pruned = prune_sparse(sparse)
    assert len(pruned) == SPARSE_MAX_NNZ
    assert min(pruned) == 501 and max(pruned) == 1500
    assert list(pruned) == sorted(pruned)   # to_sparsevec_literal ждёт порядок


class FatModel:
    """Модель, чей sparse-выход не влезает в лимит HNSW pgvector."""

    def encode(self, texts, **kw):
        return {
            "dense_vecs": [[0.1] * settings.embed_dim for _ in texts],
            "lexical_weights": [{str(i): i / 3000 for i in range(1, 1501)}
                                for _ in texts],
        }


def test_encode_texts_prunes_sparse_to_the_index_limit():
    [encoded] = encode_texts(["длинное объявление"], model=FatModel())
    assert len(encoded["sparse"]) == SPARSE_MAX_NNZ


class FakeModel:
    def encode(self, texts, **kw):
        return {
            "dense_vecs": [[0.1] * settings.embed_dim for _ in texts],
            "lexical_weights": [{"5": 0.7, "100": 0.3} for _ in texts],
        }


def test_embed_pending_only_changed():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, doc_text)
                           VALUES ('E1','kaggle','2-комн, тихо');""")
        conn.commit()
        n1 = embed_pending(conn, model=FakeModel())
        n2 = embed_pending(conn, model=FakeModel())  # hash совпал → 0
        with conn.cursor() as cur:
            cur.execute("SELECT embedding IS NOT NULL, sparse_embedding IS NOT NULL "
                        "FROM listings WHERE external_id='E1';")
            has_dense, has_sparse = cur.fetchone()
        assert n1 == 1 and n2 == 0
        assert has_dense and has_sparse
