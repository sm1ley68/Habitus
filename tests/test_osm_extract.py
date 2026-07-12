import psycopg
import requests
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.osm_extract import HEADERS, fetch_kind, parse_overpass, upsert_poi

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
