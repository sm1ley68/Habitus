from pathlib import Path
import psycopg
from habitus.ingest.cian_loader import parse_csv, parse_metro
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


def test_parse_metro_normalizes_entries():
    raw = ('[{"name":"Ленинский проспект","time":7,"transport_type":"walk"},'
           '{"name":"Шаболовская","time":3,"transport_type":"transport"}]')
    assert parse_metro(raw) == [
        {"name": "Ленинский проспект", "minutes": 7, "mode": "walk"},
        {"name": "Шаболовская", "minutes": 3, "mode": "transport"},
    ]


def test_parse_metro_survives_broken_input():
    # Битый JSON не должен ронять строку: объявление грузится, работает OSM-фолбэк
    for raw in ("", "   ", "не json", "{}", "null", None):
        assert parse_metro(raw) == []


def test_parse_csv_keeps_address_url_and_extra():
    rows = parse_csv(FIX)
    row = rows[0]
    assert row["city"] == "msk"
    assert row["address"]
    assert row["source_url"].startswith("http")
    assert isinstance(row["source_extra"]["metro"], list)
    assert "zhk" in row["source_extra"]


def test_load_to_raw_updates_columns_added_later():
    """Строка, загруженная до появления колонок Task 3, при повторном прогоне
    должна получить адрес/url/extra. Иначе перезаливка «проходит», а адреса
    в raw остаются NULL навсегда — ровно то, что случилось на dev-БД."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("DELETE FROM raw_listings WHERE source='cian';")
        conn.commit()
        rows = parse_csv(FIX)
        stale = [{**r, "address": None, "source_url": None, "source_extra": {}}
                 for r in rows]
        load_to_raw(stale, conn)          # как будто грузили до Task 3
        load_to_raw(rows, conn)           # перезаливка теми же id, но с адресом
        with conn.cursor() as cur:
            cur.execute("SELECT address, source_url, source_extra FROM raw_listings "
                        "WHERE external_id=%s;", (rows[0]["external_id"],))
            address, url, extra = cur.fetchone()
        assert address == rows[0]["address"]
        assert url == rows[0]["source_url"]
        assert extra.get("metro"), "нормализованное метро должно доехать до raw"


def test_parse_csv_reads_photos():
    rows = parse_csv(FIX)
    assert rows[0]["photos"] == ["https://cdn.cian.site/1-a.jpg",
                                 "https://cdn.cian.site/1-b.jpg"]
    assert rows[1]["photos"] == []          # пустой список, не None


def test_parse_csv_survives_broken_photos(tmp_path):
    # Битый JSON в колонке не должен ронять загрузку всей выгрузки
    src = FIX.read_text(encoding="utf-8").replace(
        '"[""https://cdn.cian.site/1-a.jpg"",""https://cdn.cian.site/1-b.jpg""]"', 'не json')
    dst = tmp_path / "broken.csv"
    dst.write_text(src, encoding="utf-8")
    assert parse_csv(dst)[0]["photos"] == []
