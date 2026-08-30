import json
from pathlib import Path

import pytest
import requests

from habitus.geo.metro import (SYSTEMS, TRANSIT_AREA, TRANSIT_RELATION_FILTER,
                               LineRaw, fetch_system, normalize_station_name,
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


def test_terminal_stations_with_entry_exit_only_roles_are_kept(payload):
    # relation 305810 (Сокольническая линия) в фикстуре: 27 node-members,
    # первый — role stop_entry_only «Бульвар Рокоссовского», последний —
    # role stop_exit_only «Потапово». Точное сравнение роли со "stop" их
    # теряет — то есть теряет ОБА конца линии молча. Проверено вручную по
    # фикстуре (см. фикс-раунд 1).
    line = parse_route_relations(payload, "subway")[0]
    assert len(line.stations) == 27
    assert line.stations[0].name == "Бульвар Рокоссовского"
    assert line.stations[-1].name == "Потапово"


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


def test_real_fixture_line_is_not_a_ring(payload):
    # Сокольническая — обычная линия, а не кольцо: первая и последняя станции
    # в фикстуре разные («Бульвар Рокоссовского» / «Потапово», см. тест выше
    # про entry/exit-only роли). Ложный ring=True на реальных данных склеил бы
    # несуществующий перегон между двумя конечными.
    line = parse_route_relations(payload, "subway")[0]
    assert line.ring is False


def test_ring_relation_gets_ring_flag_and_dedups_closing_repeat():
    # МЦК/Кольцевая в OSM: первая station-нода релейшена повторена последней —
    # так замыкают кольцо на схеме. Дедуп по нормализованному имени (см.
    # normalize_station_name) снимает этот повтор из списка станций, поэтому
    # ring нужно определять ДО дедупа, по сырой последовательности members
    # (см. R24 в task-6-brief).
    payload = {"elements": [
        {"type": "node", "id": 1, "lat": 55.75, "lon": 37.60, "tags": {"name": "А"}},
        {"type": "node", "id": 2, "lat": 55.76, "lon": 37.61, "tags": {"name": "Б"}},
        {"type": "node", "id": 3, "lat": 55.77, "lon": 37.62, "tags": {"name": "В"}},
        {"type": "node", "id": 4, "lat": 55.75, "lon": 37.60, "tags": {"name": "А"}},
        {"type": "relation", "id": 2000, "tags": {"route": "train", "ref": "14"},
         "members": [
             {"type": "node", "ref": 1, "role": "stop"},
             {"type": "node", "ref": 2, "role": "stop"},
             {"type": "node", "ref": 3, "role": "stop"},
             {"type": "node", "ref": 4, "role": "stop"},
         ]},
    ]}
    lines = parse_route_relations(payload, "mck")
    assert len(lines) == 1
    line = lines[0]
    assert line.ring is True
    assert [s.name for s in line.stations] == ["А", "Б", "В"]


def test_non_ring_relation_does_not_get_ring_flag():
    # Три РАЗНЫЕ станции, первая и последняя не совпадают — обычная линия,
    # закрывающего ребра быть не должно (см. R24: инвентированный перегон на
    # прямой линии — такая же фабрикация факта, как и пропущенный).
    payload = {"elements": [
        {"type": "node", "id": 1, "lat": 55.75, "lon": 37.60, "tags": {"name": "А"}},
        {"type": "node", "id": 2, "lat": 55.76, "lon": 37.61, "tags": {"name": "Б"}},
        {"type": "node", "id": 3, "lat": 55.77, "lon": 37.62, "tags": {"name": "В"}},
        {"type": "relation", "id": 2001, "tags": {"route": "subway", "ref": "1"},
         "members": [
             {"type": "node", "ref": 1, "role": "stop"},
             {"type": "node", "ref": 2, "role": "stop"},
             {"type": "node", "ref": 3, "role": "stop"},
         ]},
    ]}
    lines = parse_route_relations(payload, "subway")
    assert len(lines) == 1
    assert lines[0].ring is False


def test_station_present_as_both_stop_and_platform_node_dedups_to_one():
    # На МЦК/МЦД фикстуры пока нет, а комментарий в metro.py прямо
    # предупреждает, что часть линий несёт только platform-роль — то есть
    # смешанный релейшен (stop-нода + platform-нода на одну физическую
    # станцию под разными osm_id) правдоподобен. Без дедупа это две
    # StationRaw на одну станцию → нулевая по длине связь в графе.
    payload = {"elements": [
        {"type": "node", "id": 1, "lat": 55.75, "lon": 37.61,
         "tags": {"name": "Тестовая"}},
        {"type": "node", "id": 2, "lat": 55.75, "lon": 37.61,
         "tags": {"name": "Тестовая"}},
        {"type": "node", "id": 3, "lat": 55.76, "lon": 37.62,
         "tags": {"name": "Соседняя"}},
        {"type": "relation", "id": 1000, "tags": {"route": "train", "ref": "14"},
         "members": [
             {"type": "node", "ref": 1, "role": "stop"},
             {"type": "node", "ref": 2, "role": "platform"},
             {"type": "node", "ref": 3, "role": "stop"},
         ]},
    ]}
    lines = parse_route_relations(payload, "mck")
    assert len(lines) == 1
    line = lines[0]
    assert len(line.stations) == 2
    assert [s.name for s in line.stations] == ["Тестовая", "Соседняя"]


class _Resp:
    def __init__(self, status=200, payload=None):
        self.status_code = status
        self._payload = payload or {"elements": []}

    def raise_for_status(self):
        if self.status_code >= 400:
            raise requests.exceptions.HTTPError(f"HTTP {self.status_code}")

    def json(self):
        return self._payload


def test_fetch_system_sends_recursive_query_and_filter():
    # `>;` — рекурсия, без неё node-members приезжают без tags.name и
    # каждая линия молча схлопывается в 0 станций (см. комментарий у
    # fetch_system). Пиновка формы запроса — единственная защита от
    # такой регрессии, сеть в тестах запрещена.
    captured = {}

    def fake_post(url, data=None, headers=None, timeout=None):
        captured["data"] = data["data"]
        return _Resp()

    fetch_system("subway", "msk", http_post=fake_post)
    q = captured["data"]
    assert "out body;>;out body geom;" in q
    assert TRANSIT_RELATION_FILTER["subway"] in q
    assert TRANSIT_AREA["msk"] in q


def test_fetch_system_retries_then_raises_runtime_error():
    calls = {"n": 0}

    def always_retry_post(url, data=None, headers=None, timeout=None):
        calls["n"] += 1
        return _Resp(status=503)  # 503 in RETRY_STATUS

    with pytest.raises(RuntimeError):
        fetch_system("subway", "msk", http_post=always_retry_post,
                     retries=2, backoff=0)
    assert calls["n"] == 2


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
