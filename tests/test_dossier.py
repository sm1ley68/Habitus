import psycopg
import pytest
from datetime import date

from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.online.dossier import (ListingEvidence, ROUTE_PROFILE,
                                    _climate_data, _family_data,
                                    _solar_samples, build_dossier,
                                    _evidence_observed_at, _table_updated_at,
                                    _secondary_blocks, _family_sources)
from habitus.online.schema import (DossierRequest, HouseholdLegIntent,
                                   HouseholdMemberIntent, ParsedQuery)


@pytest.fixture
def dossier_conn():
    """Одна строка listings с external_id='A' — по образцу фикстуры `conn`
    из tests/test_metro_access_db.py. Метро-таблицы не нужны: тесты этого
    файла подменяют door_to_door, в БД реальный граф не ходит."""
    with psycopg.connect(settings.db_dsn) as c:
        init_db(c)
        with c.cursor() as cur:
            cur.execute("TRUNCATE listings CASCADE;")
            cur.execute("""INSERT INTO listings (external_id, source, is_active,
                               city, geom)
                           VALUES ('A','test',TRUE,'msk',
                                   ST_SetSRID(ST_MakePoint(37.60,55.75),4326));""")
        c.commit()
        yield c


class RouteProvider:
    def __init__(self):
        self.calls = []

    def directions(self, start, end, mode="foot-walking"):
        self.calls.append((start, end, mode))
        return ({"type": "LineString", "coordinates": [list(start), list(end)]}, 660)


def test_family_data_uses_explicit_time_and_ors_geometry():
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "son", "label": "Сын", "legs": [{
            "to_label": "Лицей 239", "to_kind": "school", "mode": "walk",
            "depart": "08:15",
        }]}],
    }))
    provider = RouteProvider()
    listing = ListingEvidence(37.6, 55.7, 7, 12, {})
    data = _family_data(None, req, listing, provider, lambda _: (37.61, 55.71))
    leg = data.members[0].legs[0]
    assert leg.arrive == "08:26" and leg.minutes == 11
    assert leg.geometry.coordinates[-1] == (37.61, 55.71)
    assert provider.calls[0][2] == "foot-walking"
    assert leg.safety == "caution"  # safety is conservative unless proven


def test_family_data_does_not_invent_time_or_public_transport_route(monkeypatch):
    # раньше mode="metro" молча выбрасывался (ROUTE_PROFILE его не знал);
    # теперь метро — реальный внутренний движок, и «нет маршрута» должно
    # прийти от самого движка (door_to_door → None), а не от отсутствия
    # профиля. Стаб возвращает None, как это делает движок при недостижимой
    # цели/неизвестном городе — досье не должно ходить в БД ради этого теста.
    from habitus.online import dossier as mod
    monkeypatch.setattr(mod, "door_to_door", lambda *a, **kw: None)

    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "parent", "label": "Родитель", "legs": [
            {"to_label": "Работа", "to_kind": "work", "mode": "car"},
            {"to_label": "Метро", "to_kind": "metro", "mode": "metro", "depart": "09:00"},
        ]}],
    }))
    assert _family_data(None, req, ListingEvidence(37.6, 55.7, None, None, {}),
                        RouteProvider(), lambda _: (37.7, 55.8)) is None


def test_family_data_rejects_geocode_outside_moscow():
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "son", "label": "Сын", "legs": [{
            "to_label": "Школа", "to_kind": "school", "mode": "walk",
            "depart": "08:00",
        }]}],
    }))
    provider = RouteProvider()
    data = _family_data(None, req, ListingEvidence(37.6, 55.7, None, None, {}),
                        provider, lambda _: (30.3, 59.9))
    assert data is None and provider.calls == []


def test_solar_samples_are_bounded_and_seasonal():
    summer = _solar_samples(55.75, 172, 180, [])
    winter = _solar_samples(55.75, 355, 180, [])
    assert summer and winter and len(summer) > len(winter)
    assert all(0 <= hour < 24 for hour in summer)


# insolation_rough заполнена у нуля объявлений (offline-колонка так и не
# вычисляется) и никогда не читается _climate_data — реальный источник блока
# «Вид и климат» это геометрия (ориентация + препятствия + облачность из NASA
# POWER), не эта колонка. Гейт блока — window_orientation: без него честная
# деградация должна случиться до любого обращения к БД.
def test_climate_data_degrades_without_orientation_data():
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery())
    listing = ListingEvidence(37.6, 55.7, 7, 12, {
        "window_orientation": None, "insolation_rough": None,
        "noise_level": None, "bar_density_500m": None,
    })
    # conn=None: _orientation возвращает None раньше первого обращения к БД —
    # деградация не должна требовать соединения вовсе
    assert _climate_data(None, req, listing, climate_provider=None) is None


class _FakeCursor:
    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def execute(self, *args, **kwargs):
        pass

    def fetchone(self):
        return (None,)          # средний уровень шума в радиусе не посчитан


class _FakeConn:
    def cursor(self, *args, **kwargs):
        return _FakeCursor()


class _FakeClimateProvider:
    def for_point(self, lon, lat):
        return 0.3               # облачность есть — блокирует именно шум


def test_climate_data_degrades_without_noise_evidence_no_synthetic_numbers():
    # ориентация есть и совпала с запросом, но подтверждённого шума в радиусе
    # нет — блок обязан деградировать до None, а не подставить 0 дБ
    req = DossierRequest(object_id="E1",
                         parsed_query=ParsedQuery(window_orientation=["SW"]))
    listing = ListingEvidence(37.6, 55.7, 7, 12, {
        "window_orientation": ["SW"], "insolation_rough": None,
    })
    result = _climate_data(_FakeConn(), req, listing, _FakeClimateProvider())
    assert result is None


# --- Task 11: метро-нога в досье --------------------------------------------

def test_metro_is_no_longer_an_unroutable_mode():
    # раньше ROUTE_PROFILE не знал metro и _family_data молча выбрасывал ногу
    assert "metro" in ROUTE_PROFILE


class _StubMetro:
    """Подменяет движок: досье не должно ходить в БД ради этого теста."""
    def __init__(self, ride, geometry):
        self.ride, self.geometry = ride, geometry
        self.calls = 0

    def __call__(self, conn, city, home, dest, walker=None):
        self.calls += 1
        return self.ride, self.geometry


def test_metro_leg_carries_the_ride_breakdown(monkeypatch, dossier_conn):
    from habitus.online import dossier as mod
    from habitus.online.schema import MetroRide, MetroSegment

    # wait_min — обязательное поле без дефолта (R67, фикс-раунд 2); значение
    # ниже не пересчитано в total_minutes намеренно — этот тест проверяет
    # проводку stub-ride через dossier, а не арифметику door_to_door (та
    # покрыта end-to-end в tests/test_metro_route.py).
    ride = MetroRide(
        walk_from_home_min=7, walk_to_dest_min=5, total_minutes=25, wait_min=2,
        segments=[MetroSegment(line_ref="1", line_name="Сокольническая",
                               system="subway", colour="#EF161E",
                               from_station="Сокольники", to_station="Охотный Ряд",
                               stops=6, minutes=13)])
    stub = _StubMetro(ride, [[37.60, 55.75], [37.62, 55.76]])
    monkeypatch.setattr(mod, "door_to_door", stub)

    req = DossierRequest(
        object_id="A", city="msk",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    payload = build_dossier(req, dossier_conn, geocoder=lambda q: (37.62, 55.76))
    block = next(b for b in payload.blocks if b.key == "family_routing")
    leg = block.data.members[0].legs[0]
    assert leg.mode == "metro"
    assert leg.metro is not None
    assert leg.minutes == leg.metro.total_minutes == 25
    assert leg.arrive == "08:25"
    assert stub.calls == 1


def test_no_graph_for_city_drops_the_block_instead_of_showing_zeros(
        monkeypatch, dossier_conn):
    from habitus.online import dossier as mod
    monkeypatch.setattr(mod, "door_to_door", lambda *a, **kw: None)

    req = DossierRequest(
        object_id="A", city="spb",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    payload = build_dossier(req, dossier_conn, geocoder=lambda q: (30.3, 59.93))
    # синтетический ноль вместо отсутствующего замера запрещён
    assert not any(b.key == "family_routing" for b in payload.blocks)


# --- Фикс-раунд 1 -----------------------------------------------------------

def test_family_data_geocodes_against_the_requests_own_city_r62():
    # R62 (фикс-раунд 1): раньше суффикс геокода был жёстко "Москва"
    # независимо от req.city — SPb-запрос находил московский адрес в 630 км
    # от дома, а метро-ветка (не гейтится bbox Москвы намеренно) выдавала бы
    # это за правдоподобный MetroRide с многочасовым пешим плечом.
    captured = []

    def spy_geocoder(query):
        captured.append(query)
        return (30.3, 59.93)   # где-то в Петербурге

    req = DossierRequest(object_id="E1", city="spb",
                         parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "son", "label": "Сын", "legs": [{
            "to_label": "Работа", "to_kind": "work", "mode": "walk",
            "depart": "08:00",
        }]}],
    }))
    _family_data(None, req, ListingEvidence(30.3, 59.93, None, None, {}),
                 RouteProvider(), spy_geocoder)
    assert captured == ["Работа, Санкт-Петербург"]


def test_family_routing_verdict_line_is_accurate_for_metro_legs(monkeypatch, dossier_conn):
    # R66 (фикс-раунд 1, п.4): метро-нога построена по графу рельсового
    # транспорта (Задача 9), а не ORS. Информация о происхождении теперь
    # в sources, а не в прозе.
    from habitus.online import dossier as mod
    from habitus.online.schema import MetroRide

    ride = MetroRide(walk_from_home_min=1, walk_to_dest_min=1,
                     total_minutes=10, wait_min=2)
    stub = _StubMetro(ride, [[37.60, 55.75], [37.62, 55.76]])
    monkeypatch.setattr(mod, "door_to_door", stub)

    req = DossierRequest(
        object_id="A", city="msk",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    payload = build_dossier(req, dossier_conn, geocoder=lambda q: (37.62, 55.76))
    block = next(b for b in payload.blocks if b.key == "family_routing")
    source_keys = {s.key for s in block.sources}
    assert "metro_graph" in source_keys


# --- сквозное ревью ветки: R93 ----------------------------------------------


def test_metro_leg_gets_the_walker_when_ors_is_configured(monkeypatch, dossier_conn):
    """R93: пешие плечи метро-ноги считаются пешей сетью ORS, если провайдер
    вообще инжектирован. Без walker'а door_to_door() красил КАЖДОЕ плечо
    оценкой по прямой — MetroRide.estimated был True всегда и переставал
    различать оценку и замер."""
    from habitus.online import dossier as mod
    from habitus.online.schema import MetroRide

    seen = {}

    def fake_door_to_door(conn, city, home, dest, walker=None):
        seen["walker"] = walker
        return (MetroRide(walk_from_home_min=3, walk_to_dest_min=2,
                          total_minutes=20, wait_min=1, segments=[]),
                [[37.60, 55.75], [37.62, 55.76]])

    monkeypatch.setattr(mod, "door_to_door", fake_door_to_door)

    class _Provider:
        def directions(self, start, end, profile):
            return {"type": "LineString", "coordinates": [list(start), list(end)]}, 300.0

    req = DossierRequest(
        object_id="A", city="msk",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    build_dossier(req, dossier_conn, route_provider=_Provider(),
                  geocoder=lambda q: (37.62, 55.76))

    walker = seen["walker"]
    assert walker is not None, "ORS настроен, а плечи всё равно считаются по прямой"
    # walker обязан ходить в ТОТ ЖЕ инжектированный провайдер, а не строить
    # второй клиент по ключу из настроек.
    assert walker((37.60, 55.75), (37.61, 55.75)) == 300.0


def test_metro_leg_has_no_walker_when_ors_is_absent(monkeypatch, dossier_conn):
    """Обратная сторона: без провайдера walker не изобретается — плечи
    деградируют до оценки по прямой, как и раньше."""
    from habitus.online import dossier as mod
    from habitus.online.schema import MetroRide

    seen = {}

    def fake_door_to_door(conn, city, home, dest, walker=None):
        seen["walker"] = walker
        return (MetroRide(walk_from_home_min=3, walk_to_dest_min=2,
                          total_minutes=20, wait_min=1, segments=[]),
                [[37.60, 55.75], [37.62, 55.76]])

    monkeypatch.setattr(mod, "door_to_door", fake_door_to_door)

    req = DossierRequest(
        object_id="A", city="msk",
        parsed_query=ParsedQuery(household=[HouseholdMemberIntent(
            id="me", label="я", legs=[HouseholdLegIntent(
                to_label="офис", to_kind="work", mode="metro", depart="08:00")])]))
    build_dossier(req, dossier_conn, geocoder=lambda q: (37.62, 55.76))
    assert seen["walker"] is None


# --- Task 1: Контракт BlockSource и честные даты -----

def test_evidence_observed_at_returns_layer_date(dossier_conn):
    with dossier_conn.cursor() as cur:
        cur.execute("TRUNCATE urban_evidence;")
        cur.execute("""INSERT INTO urban_evidence
                           (source_id, source, city, layer, geom, db, observed_at)
                       VALUES ('n1','test','msk','noise',
                               ST_SetSRID(ST_MakePoint(37.60,55.75),4326),
                               55, '2026-05-01');""")
    dossier_conn.commit()
    assert _evidence_observed_at(
        dossier_conn, "noise", 37.60, 55.75, "msk") == date(2026, 5, 1)


def test_evidence_observed_at_is_none_when_nothing_in_radius(dossier_conn):
    """Пустой слой даёт None, а не сегодняшнюю дату. Подставленная дата —
    это синтетическое значение вместо отсутствующего замера."""
    with dossier_conn.cursor() as cur:
        cur.execute("TRUNCATE urban_evidence;")
    dossier_conn.commit()
    assert _evidence_observed_at(
        dossier_conn, "noise", 37.60, 55.75, "msk") is None


def test_table_updated_at_refuses_unknown_table(dossier_conn):
    """Имя таблицы подставляется в SQL строкой, поэтому список закрытый."""
    assert _table_updated_at(dossier_conn, "listings; DROP TABLE poi") is None


# --- Task 2: Источники вторичных блоков -----

def test_secondary_logistics_declares_computation_over_poi():
    blocks = _secondary_blocks({"walk_min_school": 8}, None, "msk")
    source = blocks[0].sources[0]
    assert source.kind == "computation"
    assert source.observed_at is None  # conn=None — дату спросить негде


def test_secondary_window_orientation_names_the_informant():
    """Сторону света извлекли из прозы объявления: наблюдение сделал
    продавец, и basis обязан это называть."""
    blocks = _secondary_blocks({"window_orientation": "S"}, None, "msk")
    source = next(s for b in blocks for s in b.sources if s.key == "window_orientation")
    assert source.kind == "observation"
    assert "продавц" in source.basis


def test_secondary_noise_is_proxy_not_computation():
    blocks = _secondary_blocks({"noise_level": "high"}, None, "msk")
    source = next(s for b in blocks for s in b.sources if s.key == "noise")
    assert source.kind == "proxy"


def test_social_block_marks_communal_and_crime_as_proxy(dossier_conn, monkeypatch):
    """Коммунальность и риск — модельные оценки. Пометить их вычислением
    значит выдать модель за замер."""
    from habitus.online import dossier as mod
    monkeypatch.setattr(mod, "_climate_data", lambda *a, **kw: None)
    monkeypatch.setattr(mod, "_family_data", lambda *a, **kw: None)
    with dossier_conn.cursor() as cur:
        cur.execute("TRUNCATE urban_evidence;")
        for layer, sid in (("communal", "c1"), ("crime", "k1")):
            cur.execute("""INSERT INTO urban_evidence
                               (source_id, source, city, layer, geom, weight, observed_at)
                           VALUES (%s,'test','msk',%s,
                                   ST_Buffer(ST_SetSRID(ST_MakePoint(37.60,55.75),4326)::geography,
                                             300)::geometry,
                                   0.4, '2026-04-10');""", (sid, layer))
    dossier_conn.commit()
    payload = build_dossier(DossierRequest(object_id="A", city="msk"), dossier_conn)
    block = next(b for b in payload.blocks if b.key == "social_environment")
    kinds = {s.key: s.kind for s in block.sources}
    assert kinds["communal"] == "proxy" and kinds["crime"] == "proxy"
    communal = next(s for s in block.sources if s.key == "communal")
    assert communal.observed_at == date(2026, 4, 10)
    # Проза больше не дублирует структуру: основание живёт в basis и только там.
    assert "по году постройки" in communal.basis
    assert "по году постройки" not in block.verdict_line


def test_view_block_separates_computed_light_from_modelled_noise():
    """Блок смешивает разное по природе: инсоляция считается по геометрии,
    шум — модель. Одна пометка на весь блок была бы враньём."""
    from habitus.online.dossier import _view_climate_sources
    sources = {s.key: s.kind for s in _view_climate_sources(None, 37.6, 55.7, "msk")}
    assert sources["solar"] == "computation"
    assert sources["noise"] == "proxy"
    assert sources["cloudiness"] == "observation"


def test_view_block_names_the_seller_as_informant_of_orientation():
    """Hero-блок существует только при известной стороне света, и вся инсоляция
    считается от неё. Наблюдение сделал продавец, а не продукт, — значит
    информант должен быть назван здесь, а не только во вторичном блоке.
    Даты у такого наблюдения нет: объявление не датирует замер."""
    from habitus.online.dossier import _view_climate_sources
    sources = {s.key: s for s in _view_climate_sources(None, 37.6, 55.7, "msk")}
    orientation = sources["window_orientation"]
    assert orientation.kind == "observation"
    assert "продавцом" in orientation.basis
    assert orientation.observed_at is None


# --- Task 5: Источники блока семейного маршрута -----

def test_family_block_declares_graph_computation_without_date():
    """Маршрут считается на месте — датировать его нечем, и выдумывать
    дату нельзя."""
    sources = {s.key: s for s in _family_sources(None, has_metro=False)}
    assert sources["road_graph"].kind == "computation"
    assert sources["road_graph"].observed_at is None
    assert "metro_graph" not in sources


def test_family_block_adds_metro_source_only_when_metro_used():
    keys = {s.key for s in _family_sources(None, has_metro=True)}
    assert keys == {"road_graph", "metro_graph"}
