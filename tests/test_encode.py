import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.embed.encode import to_sparsevec_literal, embed_pending


def test_to_sparsevec_literal():
    lit = to_sparsevec_literal({5: 0.7, 100: 0.3}, dim=250002)
    assert lit == "{5:0.7,100:0.3}/250002"


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
