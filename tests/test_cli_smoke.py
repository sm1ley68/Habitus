import psycopg
from pathlib import Path
from habitus.config import settings
from habitus.cli import run_offline
from habitus.geo.osm_extract import POI_KINDS

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


def test_osm_failure_does_not_abort_the_cycle(monkeypatch):
    """Overpass отваливается регулярно (504 и таймауты положили 4 цикла подряд).
    Точки города меняются раз в месяцы, а объявления — каждый час: сбой чужого
    API не имеет права уносить с собой заливку, обогащение и эмбеддинги."""
    import habitus.cli as cli

    def boom(kind, city):
        raise RuntimeError(f"Overpass '{kind}' не удался: HTTP 504")

    monkeypatch.setattr(cli, "fetch_kind", boom)
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS listings, raw_listings, poi CASCADE;")
        conn.commit()
        stats = run_offline(FIX, conn, model=FakeModel(), fetch_osm=True,
                            geocoder=_no_network_geocoder)
    assert stats["listings"] == 2       # цикл дошёл до конца
    assert stats["embedded"] == 2       # и эмбеддинги посчитаны
    assert stats["osm_failed"]          # но провал зафиксирован, а не скрыт
    # запись должна нести настоящий 504 конкретного kind/city, а не проглоченный
    # TypeError от несовпавшей сигнатуры стаба — иначе тест зелёный по ошибке
    assert len(stats["osm_failed"]) == len(POI_KINDS)
    for kind, entry in zip(POI_KINDS, stats["osm_failed"]):
        assert entry.startswith(f"{kind}/msk: ")
        assert "504" in entry
