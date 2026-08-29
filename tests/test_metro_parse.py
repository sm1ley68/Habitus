import json
from pathlib import Path

import pytest

from habitus.geo.metro import (SYSTEMS, LineRaw, normalize_station_name,
                               parse_route_relations)

FIXTURE = Path(__file__).parent / "fixtures" / "overpass_subway_msk.json"


@pytest.fixture
def payload() -> dict:
    return json.loads(FIXTURE.read_text())


def test_systems_cover_all_three():
    assert SYSTEMS == ("subway", "mck", "mcd")


def test_parses_line_identity(payload):
    lines = parse_route_relations(payload, "subway")
    assert lines, "фикстура должна давать хотя бы одну линию"
    line = lines[0]
    assert isinstance(line, LineRaw)
    assert line.system == "subway"
    assert line.ref
    assert line.colour is None or line.colour.startswith("#")


def test_stations_keep_relation_order(payload):
    line = parse_route_relations(payload, "subway")[0]
    assert len(line.stations) >= 2
    # порядок следования — это порядок members релейшена, а не сортировка
    ids = [s.osm_id for s in line.stations]
    assert ids == list(dict.fromkeys(ids)), "дубликаты станций не допускаются"
    assert all(s.name for s in line.stations), "безымянная станция — мусор"
    assert all(-180 <= s.lon <= 180 and -90 <= s.lat <= 90 for s in line.stations)


def test_geometry_is_lng_lat_pairs(payload):
    line = parse_route_relations(payload, "subway")[0]
    assert all(len(p) == 2 for p in line.geometry)
    # [lng, lat]: в Москве долгота ~37, широта ~55 — перепутанный порядок видно сразу
    lng, lat = line.geometry[0]
    assert 30 < lng < 45 and 50 < lat < 60


def test_relation_without_stops_is_skipped():
    payload = {"elements": [
        {"type": "relation", "id": 1, "tags": {"route": "subway", "ref": "9"},
         "members": []},
    ]}
    assert parse_route_relations(payload, "subway") == []


@pytest.mark.parametrize("raw,expected", [
    ("Охотный Ряд", "охотный ряд"),
    ("охотный  ряд", "охотный ряд"),
    ("Тёплый Стан", "теплый стан"),
    ("Теплый Стан", "теплый стан"),
    ("Улица 1905 года", "улица 1905 года"),
    ("Библиотека имени Ленина", "библиотека имени ленина"),
    ("Ховрино ", "ховрино"),
    ("Петровско-Разумовская", "петровско-разумовская"),
])
def test_name_normalization(raw, expected):
    assert normalize_station_name(raw) == expected
