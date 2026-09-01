import psycopg
import pytest

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.metro_route import (clear_graph_cache, metro_predicate,
                                        metro_predicate_with_note)


@pytest.fixture
def conn():
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        clear_graph_cache()
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings CASCADE;")
            cur.execute("TRUNCATE metro_line CASCADE;")
            cur.execute("""INSERT INTO metro_line
                (id, city, system, ref, name, headway_s, fallback_speed_kmh)
                VALUES (1,'msk','subway','1','л1',120,40);""")
            # A —600с— B —600с— C, цель у C
            cur.execute("""INSERT INTO metro_station
                (id, city, line_id, name, name_norm, geom, order_index) VALUES
                (10,'msk',1,'A','a',ST_SetSRID(ST_MakePoint(37.50,55.75),4326),0),
                (11,'msk',1,'B','b',ST_SetSRID(ST_MakePoint(37.60,55.75),4326),1),
                (12,'msk',1,'C','c',ST_SetSRID(ST_MakePoint(37.70,55.75),4326),2);""")
            cur.execute("""INSERT INTO metro_edge
                (city, from_station, to_station, seconds) VALUES
                ('msk',10,11,600),('msk',11,10,600),
                ('msk',11,12,600),('msk',12,11,600);""")
            # BLIZKO живёт у B (10 мин езды до C), DALEKO — у A (20 мин)
            for eid, lon in (("BLIZKO", 37.60), ("DALEKO", 37.50)):
                cur.execute("""INSERT INTO listings
                    (external_id, source, is_active, city, geom)
                    VALUES (%s,'test',TRUE,'msk',
                            ST_SetSRID(ST_MakePoint(%s,55.75),4326));""", (eid, lon))
            cur.execute("""INSERT INTO listing_metro_access
                (external_id, station_id, walk_seconds) VALUES
                ('BLIZKO',11,120),('DALEKO',10,120);""")
        c.commit()
        yield c


def _ids(conn, sql, params) -> set[str]:
    rows = conn.execute(
        f"SELECT external_id FROM listings WHERE {sql}", params).fetchall()
    return {r[0] for r in rows}


def test_predicate_keeps_only_listings_within_the_budget(conn):
    # цель — у станции C; 15 минут хватает от B, но не от A
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)
    assert _ids(conn, sql, params) == {"BLIZKO"}


def test_wider_budget_admits_the_far_one(conn):
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=40)
    assert _ids(conn, sql, params) == {"BLIZKO", "DALEKO"}


def test_walk_leg_counts_towards_the_budget(conn):
    # плечо DALEKO раздуто до 20 минут — в 40-минутный бюджет он больше не лезет
    conn.execute("UPDATE listing_metro_access SET walk_seconds = 1200 "
                 "WHERE external_id = 'DALEKO';")
    conn.commit()
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=40)
    assert _ids(conn, sql, params) == {"BLIZKO"}


def test_no_graph_for_city_returns_none(conn):
    assert metro_predicate(conn, "spb", 30.3, 59.93, minutes=40) is None


def test_predicate_is_fully_parameterized(conn):
    sql, params = metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)
    # никакой склейки значений в текст запроса — только плейсхолдеры
    assert "%s" in sql and str(15 * 60) not in sql


def test_point_beyond_entry_cap_returns_none_not_crash(conn):
    # R71: точка в открытом море — дальше MAX_ENTRY_WALK_METRES (3 км) от
    # любой платформы графа msk. nearest_stations() отдаёт пустой словарь
    # (R68), и metro_predicate обязан вернуть None здесь же, а не пропустить
    # пустой times/targets дальше в VALUES-джойн (иначе "JOIN (VALUES )" —
    # синтаксическая ошибка psycopg на пустом списке пар).
    assert metro_predicate(conn, "msk", 30.0, 55.75, minutes=40) is None


# --- сквозное ревью ветки: R90 (разорванный граф) и R91 (нет плеч) ----------


def _add_isolated_line(conn, share_listings: int) -> None:
    """Вторая линия без единой пересадки с первой + объявления на ней.

    Линия стоит в ~12 км от цели теста — дальше MAX_ENTRY_WALK_METRES,
    иначе её платформы попали бы в СЕМЕНА обхода вместе с целевыми, обход
    пошёл бы сразу из обеих компонент, и разрыва графа тест бы не увидел.

    Ровно та ситуация, что нашлась на живых данных: в Москве это Бутовская
    линия (её пересадка — «Битцевский парк» ↔ «Новоясеневская», станции
    РАЗНОИМЁННЫЕ, и self-join по имени её не видит), в Петербурге — весь
    город целиком (0 строк в metro_transfer).
    """
    with conn.cursor() as cur:
        cur.execute("""INSERT INTO metro_line
            (id, city, system, ref, name, headway_s, fallback_speed_kmh)
            VALUES (2,'msk','subway','2','л2',120,40);""")
        cur.execute("""INSERT INTO metro_station
            (id, city, line_id, name, name_norm, geom, order_index) VALUES
            (20,'msk',2,'X','x',ST_SetSRID(ST_MakePoint(37.90,55.75),4326),0),
            (21,'msk',2,'Y','y',ST_SetSRID(ST_MakePoint(37.91,55.75),4326),1);""")
        cur.execute("""INSERT INTO metro_edge
            (city, from_station, to_station, seconds) VALUES
            ('msk',20,21,600),('msk',21,20,600);""")
        for i in range(share_listings):
            eid = f"OSTROV{i}"
            cur.execute("""INSERT INTO listings
                (external_id, source, is_active, city, geom)
                VALUES (%s,'test',TRUE,'msk',
                        ST_SetSRID(ST_MakePoint(37.90,55.75),4326));""", (eid,))
            cur.execute("""INSERT INTO listing_metro_access
                (external_id, station_id, walk_seconds) VALUES (%s,20,120);""",
                (eid,))
    conn.commit()
    clear_graph_cache()


def test_disconnected_graph_drops_the_filter_with_a_note(conn):
    """R90 (блокер сквозного ревью): цель на одной компоненте, а больше доли
    MAX_UNJUDGED_SHARE объявлений города — на другой. Для них время до цели
    не «больше N минут», а НЕИЗВЕСТНО: пересадки между линиями в данных нет.
    Предикат такой разницы не знает и выбросил бы их молча, поэтому фильтр
    снимается целиком, а пользователь получает заметку."""
    _add_isolated_line(conn, share_listings=5)   # 5 из 7 — сильно больше 10%
    got, note = metro_predicate_with_note(conn, "msk", 37.70, 55.75, minutes=40)
    assert got is None
    assert "разорван" in note and "5" in note


def test_small_disconnected_fragment_keeps_the_filter_but_still_notes_it(conn):
    """Обратная сторона того же порога: на живых данных Москвы изолирована
    одна Бутовская линия — 43 объявления из 6738 (0.6%). Снимать из-за них
    фильтр по всему городу значило бы выключить метро-поиск Москвы целиком;
    фильтр применяется, но молчать о неоценённых объявлениях всё равно
    нельзя."""
    _add_isolated_line(conn, share_listings=0)
    with conn.cursor() as cur:
        cur.execute("""INSERT INTO listings (external_id, source, is_active, city, geom)
                       VALUES ('OSTROV','test',TRUE,'msk',
                               ST_SetSRID(ST_MakePoint(37.90,55.75),4326));""")
        cur.execute("""INSERT INTO listing_metro_access
                       (external_id, station_id, walk_seconds) VALUES ('OSTROV',20,120);""")
        # 19 объявлений на достижимой линии: 1 из 20 = 5% < MAX_UNJUDGED_SHARE
        for i in range(18):
            cur.execute("""INSERT INTO listings (external_id, source, is_active, city, geom)
                           VALUES (%s,'test',TRUE,'msk',
                                   ST_SetSRID(ST_MakePoint(37.60,55.75),4326));""",
                        (f"BLIZKO{i}",))
            cur.execute("""INSERT INTO listing_metro_access
                           (external_id, station_id, walk_seconds)
                           VALUES (%s,11,120);""", (f"BLIZKO{i}",))
    conn.commit()

    got, note = metro_predicate_with_note(conn, "msk", 37.70, 55.75, minutes=40)
    assert got is not None
    assert "OSTROV" not in _ids(conn, got[0], got[1])
    assert note is not None and "(1 из 21) не оценена" in note


def test_city_without_access_rows_returns_none_with_a_note(conn):
    """R91: граф есть, платформы у цели есть, а пеших плеч по городу нет ни
    одного — сборка графа в habitus/cli.py коммитится, а упавший
    refresh_listing_metro_access откатывается, оставляя каскадно вычищенные
    строки доступа. Раньше это было неотличимо от «ничего не укладывается в
    бюджет»: пустая выдача без единой заметки."""
    conn.execute("TRUNCATE listing_metro_access;")
    conn.commit()
    got, note = metro_predicate_with_note(conn, "msk", 37.70, 55.75, minutes=40)
    assert got is None
    assert "плечи" in note


def test_thin_wrapper_still_returns_just_the_predicate(conn):
    """Старый контракт metro_predicate() не изменился — обёртка над версией
    с заметкой возвращает ровно кортеж (sql, params) или None."""
    assert metro_predicate(conn, "msk", 37.70, 55.75, minutes=15)[0].startswith(
        "external_id IN")
    assert metro_predicate(conn, "spb", 30.3, 59.93, minutes=40) is None
