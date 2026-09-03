import pytest
import yaml
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.eval.curate import (MIN_PRICE_PER_SQM, TOP_N, where_and_params,
                                 curate, eligible_rows)


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


def test_curate_flag_marks_queries_buildable_by_rules(tmp_path, monkeypatch):
    """Запрос без эталона, но с флагом curate, обязан курироваться правилами.

    Раньше признаком «курировать» служило наличие уже проставленных
    relevant_ids — то есть новый запрос нельзя было разметить автоматически,
    даже когда он целиком выражается условиями по колонкам (комнаты, бюджет,
    площадь, минуты). Из-за этого b-серия годами оставалась пустой.
    """
    from habitus.eval import curate as mod

    golden = [
        {"id": "b01", "query": "двушка до 35 млн", "curate": True,
         "expected_parse": {"rooms": [2], "price_max": 35000000},
         "relevant_ids": []},
        {"id": "b05", "query": "loft with brick walls",
         "expected_parse": {}, "relevant_ids": []},
    ]
    path = tmp_path / "queries.yaml"
    path.write_text(yaml.safe_dump(golden, allow_unicode=True), encoding="utf-8")
    monkeypatch.setattr(mod, "GOLDEN", path)

    seen = []

    def fake_curate(conn, item):
        seen.append(item["id"])
        return ["cian_1"], {"cian_1": 3}, 1

    monkeypatch.setattr(mod, "curate", fake_curate)
    mod.main()

    assert seen == ["b01"]          # структурный — курируется
    out = yaml.safe_load(path.read_text(encoding="utf-8"))
    by_id = {x["id"]: x for x in out}
    assert by_id["b01"]["relevant_ids"] == ["cian_1"]
    assert by_id["b05"]["relevant_ids"] == []   # семантику правилами не выдумываем


def test_match_address_ilike_is_parameterized():
    # SQL-фрагмент содержит только плейсхолдер, значение уезжает в params —
    # никакой склейки строк с текстом паттерна.
    where, params = where_and_params({}, {"address_ilike": "%Хамовник%"})
    assert "address ILIKE %s" in where
    assert not any("Хамовник" in clause for clause in where)
    assert params[-1] == "%Хамовник%"


def test_match_metro_ilike_is_parameterized():
    where, params = where_and_params({}, {"metro_ilike": "%Павелецк%"})
    assert "metro_station ILIKE %s" in where
    assert not any("Павелецк" in clause for clause in where)
    assert params[-1] == "%Павелецк%"


def test_match_combines_both_and_ignores_absent_keys():
    where, params = where_and_params(
        {"rooms": [2]}, {"address_ilike": "%Пресн%", "metro_ilike": "%Белорусск%"})
    assert where.count("address ILIKE %s") == 1
    assert where.count("metro_station ILIKE %s") == 1
    # порядок params обязан совпадать с порядком клауз: rooms, потом адрес, потом метро
    assert params == [[2], "%Пресн%", "%Белорусск%"]


def test_match_none_and_empty_dict_add_no_clauses():
    where_none, params_none = where_and_params({"rooms": [1]}, None)
    where_empty, params_empty = where_and_params({"rooms": [1]}, {})
    assert where_none == where_empty
    assert params_none == params_empty
    assert not any("ILIKE" in clause for clause in where_none)


def test_eligible_rows_filters_by_address_and_metro_end_to_end():
    """`match` действительно режет пул в БД, не только собирает клаузы."""
    with psycopg.connect(settings.db_dsn) as conn:
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            cur.execute("DELETE FROM urban_evidence;")
            rows = [
                ("in_hamovniki", "Москва, Хамовники, Комсомольский проспект", None),
                ("elsewhere", "Москва, Марьина роща", None),
                ("near_pavelets", None, "Павелецкая"),
                ("near_belorus", None, "Белорусская"),
            ]
            for eid, address, metro in rows:
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, city,
                           price, area, rooms, address, metro_station, geom)
                       VALUES (%s,'t',TRUE,'msk',20000000,50.0,2,%s,%s,
                               ST_SetSRID(ST_MakePoint(37.6,55.75),4326));""",
                    (eid, address, metro))
        conn.commit()

        addr_ids = {r["external_id"] for r in
                   eligible_rows(conn, {}, {"address_ilike": "%Хамовник%"})}
        metro_ids = {r["external_id"] for r in
                    eligible_rows(conn, {}, {"metro_ilike": "%Павелецк%"})}
    assert addr_ids == {"in_hamovniki"}
    assert metro_ids == {"near_pavelets"}


def test_unknown_match_key_raises():
    # Опечатка `adress_ilike` не должна молча дать эталон вообще без текстового
    # фильтра — это ровно тот фиктивный эталон, против которого правило
    # «не выдумывать факты».
    with pytest.raises(ValueError, match="неизвестные ключи match"):
        where_and_params({}, {"adress_ilike": "%Хамовник%"})


def test_unknown_geo_kind_raises():
    # Имя колонки walk_min_* собирается из YAML — оно обязано быть из белого
    # списка, а не любой строкой, попадающей в текст SQL.
    with pytest.raises(ValueError, match="неизвестная гео-ось"):
        where_and_params({"geo": [{"kind": "airport", "walk_minutes": 10}]})


def test_degenerate_score_keeps_whole_pool_relevant():
    """Запрос без geo и noise_max не имеет ранжирующего сигнала: срезать пул до
    TOP_N значит объявить произвольные 10 наименьших id «лучшими», а десятки
    одинаково подходящих объектов — нерелевантными."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for i in range(25):
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, city,
                           price, rooms, area, geom)
                       VALUES (%s,'test',TRUE,'msk',20000000,2,50,
                               ST_SetSRID(ST_MakePoint(37.6,55.75),4326));""",
                    (f"id_{i:03d}",))
        conn.commit()

        ids, grades, total = curate(conn, {"expected_parse": {"rooms": [2]}})

    assert total == 25
    assert len(ids) == 25                      # пул не срезан до TOP_N
    assert set(grades.values()) == {1}         # порядок ничего не значит — оценка одна
    assert ids == sorted(ids)                  # воспроизводимо


def test_ranked_score_still_cuts_to_top_n():
    """Обратная сторона: когда скор различает объекты, эталон по-прежнему
    top-10 с градациями 3/2/1, иначе NDCG перестанет что-либо мерить."""
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            for i in range(25):
                cur.execute(
                    """INSERT INTO listings (external_id, source, is_active, city,
                           price, rooms, area, walk_min_metro, geom)
                       VALUES (%s,'test',TRUE,'msk',20000000,2,50,%s,
                               ST_SetSRID(ST_MakePoint(37.6,55.75),4326));""",
                    (f"id_{i:03d}", 1 + i % 9))
        conn.commit()

        ids, grades, total = curate(
            conn, {"expected_parse": {"rooms": [2],
                                      "geo": [{"kind": "metro", "walk_minutes": 10}]}})

    assert total == 25
    assert len(ids) == TOP_N
    assert set(grades.values()) == {3, 2, 1}


# --- d-серия: эталон учитывает точки домохозяйства ------------------------
# Без этого правила новые сценарные запросы нельзя было бы разметить иначе,
# как руками, а размеченный на глаз эталон не воспроизводится.

def _seed_household(conn):
    """Три двушки на одной широте, но на разной долготе — от точек семьи
    (37.50 и 37.70) равноудалена средняя."""
    with conn.cursor() as cur:
        cur.execute("TRUNCATE listings;")
        cur.execute("DELETE FROM urban_evidence;")
        for eid, lon in (("west", 37.50), ("between", 37.60), ("east", 37.70)):
            cur.execute(
                """INSERT INTO listings (external_id, source, is_active, city, price,
                       area, rooms, geom)
                   VALUES (%s,'t',TRUE,'msk',20000000,50.0,2,
                           ST_SetSRID(ST_MakePoint(%s,55.75),4326));""",
                (eid, lon))
    conn.commit()


HOUSEHOLD_ITEM = {
    "expected_parse": {"rooms": [2], "price_max": 40_000_000},
    "household_points": [[37.50, 55.75], [37.70, 55.75]],
}


def test_curate_prefers_object_between_family_places():
    with psycopg.connect(settings.db_dsn) as conn:
        _seed_household(conn)
        ids, grades, pool = curate(conn, HOUSEHOLD_ITEM)
        again, _, _ = curate(conn, HOUSEHOLD_ITEM)
    assert ids[0] == "between"     # компромисс между двумя офисами — лучший ответ
    assert ids == again            # эталон воспроизводим
    assert pool == 3


def test_curate_without_household_points_ignores_location():
    """Тот же посев без household_points не имеет ранжирующего сигнала вовсе —
    значит релевантен весь пул. Так проверяется, что новая ось включается
    только там, где семья действительно названа."""
    with psycopg.connect(settings.db_dsn) as conn:
        _seed_household(conn)
        ids, grades, pool = curate(conn, {"expected_parse": HOUSEHOLD_ITEM["expected_parse"]})
    assert sorted(ids) == ["between", "east", "west"]
    assert set(grades.values()) == {1}
