from pathlib import Path
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
from habitus.db.init_db import init_db
import psycopg
from habitus.config import settings

FIX = Path(__file__).parent / "fixtures" / "sample_russia_realestate.csv"

def test_parse_filters_moscow_only():
    rows = parse_csv(FIX)
    assert len(rows) == 2  # питерская строка отфильтрована
    assert all(r["source"] == "kaggle" for r in rows)
    first = rows[0]
    assert first["rooms"] == 2
    assert first["price"] == 12000000
    assert abs(first["lat"] - 55.7558) < 1e-4
    assert first["external_id"]  # непустой стабильный id

def test_parse_stable_external_id():
    rows1 = parse_csv(FIX)
    rows2 = parse_csv(FIX)
    assert rows1[0]["external_id"] == rows2[0]["external_id"]

def test_load_to_raw_upsert_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        rows = parse_csv(FIX)
        n1 = load_to_raw(rows, conn)
        n2 = load_to_raw(rows, conn)  # повтор
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM raw_listings WHERE source='kaggle';")
            total = cur.fetchone()[0]
        assert n1 == 2
        assert total == 2  # не задвоилось
