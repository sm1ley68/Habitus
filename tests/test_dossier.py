from habitus.online.dossier import (ListingEvidence, _climate_data,
                                    _family_data, _solar_samples)
from habitus.online.schema import DossierRequest, ParsedQuery


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
    data = _family_data(req, listing, provider, lambda _: (37.61, 55.71))
    leg = data.members[0].legs[0]
    assert leg.arrive == "08:26" and leg.minutes == 11
    assert leg.geometry.coordinates[-1] == (37.61, 55.71)
    assert provider.calls[0][2] == "foot-walking"
    assert leg.safety == "caution"  # safety is conservative unless proven


def test_family_data_does_not_invent_time_or_public_transport_route():
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "parent", "label": "Родитель", "legs": [
            {"to_label": "Работа", "to_kind": "work", "mode": "car"},
            {"to_label": "Метро", "to_kind": "metro", "mode": "metro", "depart": "09:00"},
        ]}],
    }))
    assert _family_data(req, ListingEvidence(37.6, 55.7, None, None, {}),
                        RouteProvider(), lambda _: (37.7, 55.8)) is None


def test_family_data_rejects_geocode_outside_moscow():
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "son", "label": "Сын", "legs": [{
            "to_label": "Школа", "to_kind": "school", "mode": "walk",
            "depart": "08:00",
        }]}],
    }))
    provider = RouteProvider()
    data = _family_data(req, ListingEvidence(37.6, 55.7, None, None, {}),
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
