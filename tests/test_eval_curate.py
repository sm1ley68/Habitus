import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.eval.curate import MIN_PRICE_PER_SQM, TOP_N, curate, eligible_rows


def _seed(conn):
    """Пять двушек: две с аномальной ценой, три нормальных с разной близостью."""
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings;")
        cur.execute("DELETE FROM urban_evidence;")
        rows = [
            # eid, price, area, rooms, walk_school
            ("near", 20_000_000, 50.0, 2, 2.0),
            ("mid", 22_000_000, 50.0, 2, 5.0),
            ("far", 24_000_000, 50.0, 2, 9.0),
            ("junk", 1_000_000, 100.0, 2, 1.0),   # 10 тыс. ₽/м² — доля или битый парс
            ("wrong_rooms", 30_000_000, 80.0, 4, 1.0),
        ]
        for eid, price, area, rooms, ws in rows:
            cur.execute(
                """INSERT INTO listings (external_id, source, is_active, city, price,
                       area, rooms, walk_min_school, geom)
                   VALUES (%s,'t',TRUE,'msk',%s,%s,%s,%s,
                           ST_SetSRID(ST_MakePoint(37.6,55.75),4326));""",
                (eid, price, area, rooms, ws))
    conn.commit()


EXPECTED = {"rooms": [2], "price_max": 40_000_000,
            "geo": [{"kind": "school", "walk_minutes": 10}]}


def test_curate_excludes_implausible_price_and_wrong_rooms():
    # Аномалия по цене ближе всех к школе, но лучшим ответом её называть нельзя:
    # 10 тыс. ₽/м² — это доля или ошибка парсинга, а не выгодная двушка.
    with psycopg.connect(settings.db_dsn) as conn:
        _seed(conn)
        ids = [r["external_id"] for r in eligible_rows(conn, EXPECTED)]
    assert "junk" not in ids
    assert "wrong_rooms" not in ids
    assert set(ids) == {"near", "mid", "far"}


def test_curate_ranks_by_closeness_and_is_deterministic():
    with psycopg.connect(settings.db_dsn) as conn:
        _seed(conn)
        ids, grades, pool = curate(conn, {"expected_parse": EXPECTED})
        again, _, _ = curate(conn, {"expected_parse": EXPECTED})
    assert ids == ["near", "mid", "far"]          # ближе к школе — выше
    assert ids == again                            # прогон воспроизводим
    assert grades["near"] > grades["far"]
    assert pool == 3
    assert len(ids) <= TOP_N


def test_curate_ignores_the_ranking_stack():
    """Эталон обязан строиться по данным, а не прогоном поиска: без эмбеддингов
    (dense/sparse каналы мертвы) результат курирования не меняется — иначе eval
    измерял бы сам себя."""
    with psycopg.connect(settings.db_dsn) as conn:
        _seed(conn)
        with conn.cursor() as cur:
            cur.execute("UPDATE listings SET embedding=NULL, sparse_embedding=NULL;")
        conn.commit()
        ids, _, _ = curate(conn, {"expected_parse": EXPECTED})
    assert ids == ["near", "mid", "far"]


def test_price_floor_is_far_below_market():
    # Отсечка должна резать аномалии, а не дешёвые окраины: медиана по базе
    # ~650 тыс. ₽/м², 5-й перцентиль ~317 тыс.
    assert MIN_PRICE_PER_SQM < 317_000
