import psycopg
from pathlib import Path
from habitus.config import settings
from habitus.cli import run_offline

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"


class FakeModel:
    def encode(self, texts, **kw):
        return {"dense_vecs": [[0.1] * settings.embed_dim for _ in texts],
                "lexical_weights": [{"5": 0.5} for _ in texts]}


def test_run_offline_end_to_end():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        stats = run_offline(FIX, conn, model=FakeModel(), fetch_osm=False)
        assert stats["raw"] == 2
        assert stats["listings"] == 2
        assert stats["embedded"] == 2
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings "
                        "WHERE embedding IS NOT NULL AND doc_text IS NOT NULL;")
            assert cur.fetchone()[0] == 2
