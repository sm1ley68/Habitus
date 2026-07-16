from pathlib import Path
import psycopg
from habitus.ingest.cian_loader import parse_csv
from habitus.ingest.kaggle_loader import load_to_raw
from habitus.db.init_db import init_db
from habitus.config import settings

FIX = Path(__file__).parent / "fixtures" / "sample_cian.csv"


def test_parse_maps_cian_columns():
    rows = parse_csv(FIX)
    assert len(rows) == 2
    assert all(r["source"] == "cian" for r in rows)
    first = rows[0]
    assert first["external_id"] == "cian_317927888"  # стабильный id из cian_id
    assert first["rooms"] == 1
    assert first["price"] == 44872500
    assert first["level"] == 2 and first["levels"] == 18  # floor/floors
    assert abs(first["lat"] - 55.71072) < 1e-4
    assert "SHIFT" in first["description"]  # проза-описание сохранена
    # специфичных для Циана колонок нет в raw-схеме — их быть не должно
    assert first["kitchen_area"] is None
    assert first["building_type"] is None


def test_load_to_raw_upsert_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("DELETE FROM raw_listings WHERE source='cian';")
        conn.commit()
        rows = parse_csv(FIX)
        n1 = load_to_raw(rows, conn)
        load_to_raw(rows, conn)  # повтор
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM raw_listings WHERE source='cian';")
            total = cur.fetchone()[0]
        assert n1 == 2
        assert total == 2  # не задвоилось
