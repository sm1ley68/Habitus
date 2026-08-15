import psycopg
from pathlib import Path
from habitus.config import settings
from habitus.cli import run_offline

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"


class FakeModel:
    def encode(self, texts, **kw):
        return {"dense_vecs": [[0.1] * settings.embed_dim for _ in texts],
                "lexical_weights": [{"5": 0.5} for _ in texts]}


def _no_network_geocoder(addr, session=None):
    raise AssertionError("geocoder не должен вызываться в smoke-тесте (нет сети)")


def test_run_offline_end_to_end():
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        stats = run_offline(FIX, conn, model=FakeModel(), fetch_osm=False,
                            geocoder=_no_network_geocoder)
        assert stats["raw"] == 2
        assert stats["listings"] == 2
        assert stats["embedded"] == 2
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings "
                        "WHERE embedding IS NOT NULL AND doc_text IS NOT NULL;")
            assert cur.fetchone()[0] == 2


CIAN_FIX = Path(__file__).parent / "fixtures" / "sample_cian.csv"


def test_run_offline_deactivates_listings_gone_from_source():
    """Повторный прогон с усечённым снимком обязан гасить пропавшее объявление —
    иначе база копит снятые с продажи квартиры и они остаются в выдаче."""
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        run_offline(CIAN_FIX, conn, model=FakeModel(), fetch_osm=False,
                    geocoder=_no_network_geocoder, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE is_active;")
            before = cur.fetchone()[0]
        assert before == 2

        # снимок, где осталось только первое объявление
        rows = CIAN_FIX.read_text(encoding="utf-8").splitlines()
        shrunk = Path(conn.info.dbname + "_shrunk.csv")
        shrunk.write_text("\n".join(rows[:2]) + "\n", encoding="utf-8")
        try:
            stats = run_offline(shrunk, conn, model=FakeModel(), fetch_osm=False,
                                geocoder=_no_network_geocoder, source="cian")
        finally:
            shrunk.unlink(missing_ok=True)
        assert stats["deactivated"] == 1
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE is_active;")
            assert cur.fetchone()[0] == 1
