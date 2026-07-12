import json
from habitus.config import settings
from habitus.online.geo import ORSProvider, midpoint, point_predicate


def test_midpoint():
    assert midpoint((37.0, 55.0), (38.0, 56.0)) == (37.5, 55.5)


def test_point_predicate_default_is_dwithin_circle():
    sql, params = point_predicate(37.6, 55.7, 15)
    assert "ST_DWithin" in sql and "geography" in sql
    assert params == (37.6, 55.7, 1200.0)      # 15 мин * 80 м/мин


class FakeIsochrone:
    def isochrone(self, lon, lat, minutes, mode="foot-walking"):
        return {"type": "Polygon",
                "coordinates": [[[37, 55], [38, 55], [38, 56], [37, 55]]]}


def test_point_predicate_with_provider_uses_polygon():
    sql, params = point_predicate(37.6, 55.7, 15, provider=FakeIsochrone())
    assert "ST_Within" in sql and "ST_GeomFromGeoJSON" in sql
    assert json.loads(params[0])["type"] == "Polygon"


class _FakeResp:
    status_code = 200
    def raise_for_status(self): pass
    def json(self):
        return {"features": [{"geometry": {"type": "Polygon",
                                           "coordinates": [[[1, 2]]]}}]}


class FakeSession:
    def __init__(self):
        self.url = self.payload = self.headers = None

    def post(self, url, json=None, headers=None, timeout=None):
        self.url, self.payload, self.headers = url, json, headers
        return _FakeResp()


def test_ors_provider_builds_request():
    s = FakeSession()
    poly = ORSProvider(session=s).isochrone(37.6, 55.7, 10)
    assert poly["type"] == "Polygon"
    assert s.url == f"{settings.ors_base_url}/v2/isochrones/foot-walking"
    assert s.payload == {"locations": [[37.6, 55.7]], "range": [600],
                         "range_type": "time"}
    assert s.headers["Authorization"] == settings.ors_api_key


import psycopg
from habitus.db.init_db import init_db
from habitus.online.retrieval import filter_only_search
from habitus.online.schema import ParsedQuery


def test_point_predicate_filters_in_postgis():
    with psycopg.connect(settings.db_dsn) as conn:
        init_db(conn)
        with conn.cursor() as cur:
            cur.execute("TRUNCATE listings;")
            # NEAR ~200 м от точки, FAR ~5 км
            cur.execute("""INSERT INTO listings (external_id, source, geom) VALUES
                ('NEAR','test', ST_SetSRID(ST_MakePoint(37.6190, 55.7560), 4326)),
                ('FAR', 'test', ST_SetSRID(ST_MakePoint(37.70,   55.80),   4326));""")
        conn.commit()
        sql, params = point_predicate(37.6173, 55.7558, 15)   # радиус 1200 м
        cands = filter_only_search(conn, ParsedQuery(), geo_sql=sql,
                                   geo_params=params)
        assert [c.external_id for c in cands] == ["NEAR"]
