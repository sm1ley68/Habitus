import psycopg
import requests
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.osm_extract import (CITY_AREA, HEADERS, POI_KINDS, TRANSIT_AREA, fetch_kind,
                                     overpass_queries, parse_overpass, parse_urban_features,
                                     upsert_poi)

SAMPLE = {"elements": [
    {"type": "node", "id": 111, "lat": 55.76, "lon": 37.62, "tags": {"name": "Бар А"}},
    {"type": "node", "id": 222, "lat": 55.77, "lon": 37.63, "tags": {}},
]}

def test_parse_overpass_maps_fields():
    rows = parse_overpass("bar", SAMPLE)
    assert len(rows) == 2
    assert rows[0] == {"osm_id": 111, "kind": "bar", "name": "Бар А",
                       "lat": 55.76, "lon": 37.62}
    assert rows[1]["name"] is None


def test_parse_overpass_way_uses_center():
    # парки — way/relation; координаты берём из center (`out center`).
    payload = {"elements": [
        {"type": "way", "id": 900, "center": {"lat": 55.80, "lon": 37.50},
         "tags": {"name": "Парк Горького"}},
        {"type": "relation", "id": 901, "center": {"lat": 55.70, "lon": 37.55},
         "tags": {}},
        {"type": "way", "id": 902, "tags": {"name": "без center"}},  # пропускаем
    ]}
    rows = parse_overpass("park", payload)
    assert len(rows) == 2
    assert rows[0] == {"osm_id": 900, "kind": "park", "name": "Парк Горького",
                       "lat": 55.80, "lon": 37.50}
    assert rows[1]["osm_id"] == 901 and rows[1]["name"] is None


def test_parse_urban_features_keeps_explicit_height_and_levels_separate():
    payload = {"elements": [{
        "type": "way", "id": 42,
        "tags": {"building": "apartments", "height": "24 m",
                 "building:levels": "8", "name": "Корпус"},
        "geometry": [
            {"lon": 37.6, "lat": 55.7}, {"lon": 37.61, "lat": 55.7},
            {"lon": 37.61, "lat": 55.71}, {"lon": 37.6, "lat": 55.7},
        ],
    }]}
    row = parse_urban_features(payload)[0]
    assert row["kind"] == "building" and row["height_m"] == 24
    assert row["levels"] == 8
    assert '"type": "Polygon"' in row["geometry"]

class _Resp:
    def __init__(self, status=200, payload=None):
        self.status_code = status
        self._payload = payload or {"elements": []}

    def raise_for_status(self):
        if self.status_code >= 400:
            raise requests.exceptions.HTTPError(f"HTTP {self.status_code}")

    def json(self):
        return self._payload


def test_fetch_kind_sends_user_agent():
    # Overpass отвечает 406 без User-Agent — фетч обязан слать заголовок.
    captured = {}

    def fake_post(url, data=None, headers=None, timeout=None):
        captured["headers"] = headers
        captured["data"] = data
        return _Resp()

    fetch_kind("bar", http_post=fake_post)
    assert captured["headers"] == HEADERS and "User-Agent" in captured["headers"]
    assert "data" in captured["data"]  # тело POST, а не query-string


def test_fetch_kind_retries_transient_504():
    # первый ответ 504 (транзиент), второй — успех: ретрай обязан вытащить.
    calls = {"n": 0}

    def flaky_post(url, data=None, headers=None, timeout=None):
        calls["n"] += 1
        if calls["n"] == 1:
            return _Resp(status=504)
        return _Resp(payload=SAMPLE)

    rows = fetch_kind("bar", http_post=flaky_post, backoff=0)
    assert calls["n"] == 2 and len(rows) == 2


def test_upsert_poi_idempotent():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE poi;")
        conn.commit()
        rows = parse_overpass("bar", SAMPLE)
        upsert_poi(rows, conn)
        upsert_poi(rows, conn)
        with conn.cursor() as cur:
            cur.execute("SELECT count(*), count(geom) FROM poi WHERE kind='bar';")
            total, with_geom = cur.fetchone()
        assert total == 2 and with_geom == 2


def test_school_query_covers_polygons():
    # Школьные здания в OSM — way/relation, а не node: node-only запрос давал
    # 173 школы на Москву вместо ~1500. Проверяем и тип элемента, и bbox —
    # запрос без bbox ушёл бы по всей планете, а сеть в тестах запрещена,
    # поэтому этот тест — единственная защита от такой регрессии.
    q = overpass_queries(CITY_AREA["msk"])["school"]
    for element in ("node", "way", "relation"):
        assert f'{element}["amenity"="school"]{CITY_AREA["msk"]};' in q
    assert q.startswith("(") and q.endswith(");")


def test_city_area_covers_both_cities():
    assert set(CITY_AREA) == {"msk", "spb"}
    # формат Overpass: (south,west,north,east)
    assert CITY_AREA["spb"] == "(59.70,29.60,60.20,30.70)"


def test_transit_area_for_moscow_is_wider_than_city():
    # диаметры уходят далеко за город; городской bbox рвал бы линию посередине
    city = [float(x) for x in CITY_AREA["msk"].strip("()").split(",")]
    transit = [float(x) for x in TRANSIT_AREA["msk"].strip("()").split(",")]
    assert transit[0] < city[0] and transit[1] < city[1]
    assert transit[2] > city[2] and transit[3] > city[3]


def test_transit_area_for_spb_equals_city_area():
    # диаметров в Петербурге нет — расширять нечего
    assert TRANSIT_AREA["spb"] == CITY_AREA["spb"]


def test_queries_are_built_for_the_requested_city():
    q = overpass_queries(CITY_AREA["spb"])
    assert set(q) == set(POI_KINDS)
    assert all(CITY_AREA["spb"] in fragment for fragment in q.values())


def test_fetch_kind_sends_the_city_bbox():
    seen = {}

    class Resp:
        status_code = 200
        def raise_for_status(self): pass
        def json(self): return {"elements": []}

    def fake_post(url, data=None, headers=None, timeout=None):
        seen["data"] = data["data"]
        return Resp()

    fetch_kind("metro", "spb", http_post=fake_post)
    assert CITY_AREA["spb"] in seen["data"]
    assert CITY_AREA["msk"] not in seen["data"]


def test_upsert_poi_sets_city():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE poi;")
        conn.commit()
        # Проверяем INSERT path с дефолтным городом
        upsert_poi([{"osm_id": 1, "kind": "school", "name": "Школа",
                     "lat": 55.75, "lon": 37.61}], conn)
        with conn.cursor() as cur:
            cur.execute("SELECT city FROM poi WHERE osm_id=1;")
            assert cur.fetchone()[0] == "msk"
        # Проверяем ON CONFLICT UPDATE path: переупсертим с другим городом
        upsert_poi([{"osm_id": 1, "kind": "school", "name": "Школа",
                     "lat": 55.75, "lon": 37.61}], conn, city="dxb")
        with conn.cursor() as cur:
            cur.execute("SELECT city FROM poi WHERE osm_id=1;")
            assert cur.fetchone()[0] == "dxb"
