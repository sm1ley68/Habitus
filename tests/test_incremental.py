import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.update.incremental import apply_new_poi, deactivate_missing
from habitus.ingest.kaggle_loader import load_to_raw
from habitus.clean.normalize import promote_to_listings


def test_new_bar_recomputes_nearby_density():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings, poi;")
            cur.execute("""INSERT INTO listings (external_id, source, geom, bar_density_500m)
                VALUES ('L1','kaggle',
                    ST_SetSRID(ST_MakePoint(37.6173,55.7558),4326), 0);""")
        conn.commit()
        new_bar = [{"osm_id": 999, "kind": "bar", "name": "Новый бар",
                    "lat": 55.7560, "lon": 37.6180}]
        affected = apply_new_poi(new_bar, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT bar_density_500m FROM listings WHERE external_id='L1';")
            density = cur.fetchone()[0]
        assert affected >= 1
        assert density == 1  # пересчиталось


def test_deactivate_missing():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("""INSERT INTO listings (external_id, source, is_active)
                           VALUES ('A','cian',true),('B','cian',true);""")
        conn.commit()
        n = deactivate_missing({"A"}, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='B';")
            assert cur.fetchone()[0] is False
        assert n == 1


def _seed_two_sources(conn):
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings;")
        for eid, src in (("cian_1", "cian"), ("cian_2", "cian"), ("kag_1", "kaggle")):
            cur.execute("""INSERT INTO listings (external_id, source, is_active, geom)
                VALUES (%s,%s,TRUE,ST_SetSRID(ST_MakePoint(37.6,55.75),4326));""",
                        (eid, src))
    conn.commit()


def test_deactivate_missing_is_scoped_to_its_source():
    """Снимок Циана не должен гасить объявления другого источника: списки
    external_id у них не пересекаются, и без скоупа один прогон убил бы всё чужое."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_two_sources(conn)
        gone = deactivate_missing({"cian_1"}, conn, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT external_id FROM listings WHERE is_active ORDER BY 1;")
            alive = [r[0] for r in cur.fetchall()]
    assert gone == 1                      # погашен только cian_2
    assert alive == ["cian_1", "kag_1"]   # чужой источник цел


def test_reappearing_listing_comes_back_active():
    """Объявление, вернувшееся в выдачу источника, должно снова стать активным —
    иначе повторно выставленная квартира навсегда пропадёт из поиска."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_two_sources(conn)
        deactivate_missing({"cian_1"}, conn, source="cian")
        row = {"external_id": "cian_2", "source": "cian", "price": 20_000_000,
               "area": 54.0, "kitchen_area": None, "rooms": 2, "level": 3,
               "levels": 9, "building_type": None, "object_type": None,
               "lat": 55.75, "lon": 37.62, "description": "снова в продаже",
               "city": "msk", "address": None, "source_url": None,
               "source_extra": {}, "photos": []}
        load_to_raw([row], conn)
        promote_to_listings(conn)
        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='cian_2';")
            assert cur.fetchone()[0] is True


# --- снимок последнего прогона -------------------------------------------
#
# CSV сборщика НАКАПЛИВАЕТСЯ: Store.load() читает существующий файл и делает
# latest-wins merge по cian_id, поэтому из файла никогда ничего не исчезает.
# Гашение, сравнивавшее базу со всем файлом, из-за этого не могло сработать
# ни разу: 6 циклов подряд давали deactivated=0 при 100% активных объявлений.
# Свежий снимок — это строки ПОСЛЕДНЕГО прогона, а не весь файл.
from habitus.update.incremental import latest_snapshot_ids


def test_latest_snapshot_keeps_only_the_last_crawl():
    rows = [
        {"external_id": "old_1", "collected_at": "2026-07-16T13:36:53Z"},
        {"external_id": "old_2", "collected_at": "2026-07-16T13:40:40Z"},
        {"external_id": "new_1", "collected_at": "2026-08-15T17:30:43Z"},
        {"external_id": "new_2", "collected_at": "2026-08-15T17:35:54Z"},
    ]
    assert latest_snapshot_ids(rows, crawls=1) == {"new_1", "new_2"}


def test_rows_without_timestamp_are_never_deactivated():
    """Kaggle-выгрузка не несёт времени сбора: без метки строка обязана попасть
    в снимок, иначе прогон другого источника погасил бы её на ровном месте."""
    rows = [{"external_id": "kag_1"}, {"external_id": "kag_2"}]
    assert latest_snapshot_ids(rows) == {"kag_1", "kag_2"}


def test_undatable_row_survives_alongside_dated_ones():
    rows = [
        {"external_id": "old", "collected_at": "2026-07-16T13:36:53Z"},
        {"external_id": "broken", "collected_at": "не дата"},
        {"external_id": "new", "collected_at": "2026-08-15T17:30:43Z"},
    ]
    got = latest_snapshot_ids(rows, crawls=1)
    assert "new" in got and "broken" in got and "old" not in got


def test_one_crawl_stays_whole_despite_minutes_of_spread():
    """Обход 1500 объявлений занимает минуты — это ОДИН прогон, а не полтора."""
    rows = [{"external_id": f"id_{i}",
             "collected_at": f"2026-08-15T17:3{i}:00Z"} for i in range(6)]
    assert len(latest_snapshot_ids(rows)) == 6


def test_empty_snapshot_deactivates_nothing():
    """Сорванный сбор не имеет права обнулить базу: пустой снимок — это
    отсутствие данных, а не сообщение «всё снято с продажи»."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        _seed_two_sources(conn)
        gone = deactivate_missing(set(), conn, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE is_active;")
            alive = cur.fetchone()[0]
    assert gone == 0
    assert alive == 3


def test_snapshot_spans_two_last_crawls():
    """Гасим по ДВУМ последним обходам, а не по одному.

    Обход обрывается на середине (сегодня — капча на 22-й странице), и по
    единственному снимку живые объявления, просто не попавшие в усечённый
    обход, выглядели бы как снятые с продажи. Объявление, встреченное в
    предыдущем обходе, переживает один сорванный.
    """
    rows = [
        {"external_id": "июль",   "collected_at": "2026-07-16T13:36:53Z"},
        {"external_id": "вчера",  "collected_at": "2026-08-15T17:30:43Z"},
        {"external_id": "сегодня","collected_at": "2026-08-17T19:23:27Z"},
    ]
    assert latest_snapshot_ids(rows, crawls=2) == {"вчера", "сегодня"}
    assert latest_snapshot_ids(rows, crawls=1) == {"сегодня"}


def test_more_crawls_requested_than_exist_keeps_everything():
    rows = [{"external_id": "a", "collected_at": "2026-08-17T19:23:27Z"}]
    assert latest_snapshot_ids(rows, crawls=5) == {"a"}


def test_partial_snapshot_deactivates_nothing():
    """Щадящий обход (HABITUS_MAX_OFFERS=400) снимает первые страницы выдачи,
    а не всю базу. Объявление, которого нет в таком снимке, скорее «не
    долистали», чем «снято с продажи»: без порога один щадящий цикл гасил бы
    всё, что собрали предыдущие, и база схлопывалась до размера снимка."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for i in range(20):
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active)
                       VALUES (%s,'cian',true);""", (f"cian_{i:02d}",))
        conn.commit()

        # снимок покрывает 2 из 20 активных — 10%, заведомо ниже порога
        gone = deactivate_missing({"cian_00", "cian_01"}, conn, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT count(*) FROM listings WHERE is_active;")
            alive = cur.fetchone()[0]
    assert gone == 0
    assert alive == 20


def test_full_snapshot_still_deactivates():
    """Обратная сторона: полный обход обязан гасить снятое с продажи, иначе
    защита от частичного снимка превращается в отключение гашения вовсе."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for i in range(10):
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active)
                       VALUES (%s,'cian',true);""", (f"cian_{i:02d}",))
        conn.commit()

        # снимок покрывает 9 из 10 — обход полный, одно объявление ушло
        snapshot = {f"cian_{i:02d}" for i in range(9)}
        gone = deactivate_missing(snapshot, conn, source="cian")
        with conn.cursor() as cur:
            cur.execute("SELECT is_active FROM listings WHERE external_id='cian_09';")
            assert cur.fetchone()[0] is False
    assert gone == 1
