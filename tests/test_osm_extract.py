import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.geo.osm_extract import parse_overpass, upsert_poi

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
