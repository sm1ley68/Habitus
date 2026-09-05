"""Метка поездки: что можно геокодировать, а что — выдумка.

Разбор реального прогона: на запрос «ребёнку в школу пешком без крупных дорог,
мне на работу в Сити на метро» NLU отдавал метки «Школа, Москва» и «Сити,
Москва». Nominatim возвращал на первую произвольную школу в Кузьминках, на
вторую — точку на севере города вместо делового центра. Дальше обе ехали в
ранжирование и в досье как настоящие факты: объект в Переделкине оказывался
«удачным», а в досье писалось «ребёнку до школы 156 минут».
"""
from habitus.online.household import geocode_leg, is_generic_label
from habitus.online.schema import HouseholdLegIntent


def _leg(label: str, kind: str = "school", mode: str = "walk") -> HouseholdLegIntent:
    return HouseholdLegIntent(to_label=label, to_kind=kind, mode=mode)


def test_generic_label_is_not_a_place():
    assert is_generic_label("Школа, Москва")
    assert is_generic_label("школа")
    assert is_generic_label("Работа")
    assert is_generic_label("парк")


def test_named_place_is_not_generic():
    # Настоящее название школы обязано геокодироваться: запрет касается класса
    # мест, а не всех школ подряд.
    assert not is_generic_label("Лицей 239")
    assert not is_generic_label("школа №57")
    assert not is_generic_label("Москва-Сити")


def test_generic_label_yields_no_point():
    """Выдуманная точка хуже отсутствующей: отсутствие видно и оговаривается."""
    called = []

    def geocoder(query):  # геокодер не должен быть вызван вовсе
        called.append(query)
        return (37.72, 55.71)

    assert geocode_leg(_leg("Школа, Москва"), "msk", geocoder) is None
    assert called == []


def test_named_place_still_geocodes():
    got = geocode_leg(_leg("Лицей 239"), "msk", lambda _: (37.72, 55.69))
    assert got == (37.72, 55.69)


def test_colloquial_landmark_resolves_to_real_one():
    """«Сити» без уточнения геокодер уводит на север города — правим алиасом."""
    seen = []

    def geocoder(query):
        seen.append(query)
        return (37.5387342, 55.7489657)

    got = geocode_leg(_leg("Сити, Москва", kind="work", mode="metro"), "msk", geocoder)
    assert got == (37.5387342, 55.7489657)
    assert seen == ["Москва-Сити"]


# --- обобщённая поездка → требование к району -------------------------------

from habitus.online.household import DEFAULT_WALK_MINUTES, district_requirements
from habitus.online.schema import GeoConstraint, ParsedQuery


def _pq(legs, geo=None):
    return ParsedQuery.model_validate({
        "geo": geo or [],
        "household": [{"id": "kid", "label": "Ребёнок", "legs": legs}],
    })


def test_generic_walk_leg_becomes_district_requirement():
    """«В школу пешком» мерится walk_min_school, а не расстоянием до случайной школы."""
    got = district_requirements(_pq([
        {"to_label": "Школа, Москва", "to_kind": "school", "mode": "walk"}]))
    assert got == [GeoConstraint(kind="school", walk_minutes=DEFAULT_WALK_MINUTES)]


def test_named_place_stays_a_point_not_a_requirement():
    # «Лицей 239» — настоящее место, у него есть координата; подменять его
    # требованием «любая школа рядом» значит терять то, что человек сказал.
    assert district_requirements(_pq([
        {"to_label": "Лицей 239", "to_kind": "school", "mode": "walk"}])) == []


def test_explicit_minutes_win_over_default():
    """Названное человеком число не перебивается принятым по умолчанию."""
    assert district_requirements(_pq(
        [{"to_label": "Школа", "to_kind": "school", "mode": "walk"}],
        geo=[{"kind": "school", "walk_minutes": 7}])) == []


def test_only_measured_categories_become_requirements():
    # У «работы» нет колонки walk_min_work — мерить нечем, требование не
    # выдумываем.
    assert district_requirements(_pq([
        {"to_label": "Работа", "to_kind": "work", "mode": "walk"}])) == []


def test_metro_trip_is_not_a_walking_requirement():
    # Поездка НА метро — это не «метро в пешей доступности».
    assert district_requirements(_pq([
        {"to_label": "Работа", "to_kind": "work", "mode": "metro"}])) == []


# --- ближайший объект вместо потерянной ноги --------------------------------

def test_generic_leg_falls_back_to_nearest_poi(monkeypatch):
    """Класс мест не выдумываем, но ближайший объект показать честно можно.

    Человек сказал «ребёнку в школу пешком». Точку «его» школы взять неоткуда,
    зато ближайшая школа — тот же измеренный факт, из которого посчитана
    колонка walk_min_school. Досье обязано показать маршрут до неё и подписать
    её ИМЕННО ближайшей, иначе выйдет, что выбор сделали за человека.
    """
    from habitus.online import dossier as mod
    from habitus.online.schema import DossierRequest, ParsedQuery

    monkeypatch.setattr(mod, "nearest_poi",
                        lambda conn, city, kind, home: ((37.74, 55.79), "Школа №1362"))
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "kid", "label": "Ребёнок", "legs": [
            {"to_label": "Школа, Москва", "to_kind": "school", "mode": "walk"}]}],
    }))
    data = mod._family_data(None, req, mod.ListingEvidence(37.745, 55.789, None, None, {}),
                            None, lambda _: (37.9, 55.9))
    leg = data.members[0].legs[0]
    assert leg.to_label == "ближайшая школа: Школа №1362"
    assert leg.minutes > 0


def test_named_place_does_not_get_replaced_by_nearest(monkeypatch):
    """Названное место остаётся собой: подменять его ближайшим нельзя."""
    from habitus.online import dossier as mod
    from habitus.online.schema import DossierRequest, ParsedQuery

    called = []
    monkeypatch.setattr(mod, "nearest_poi",
                        lambda *a, **kw: called.append(a) or ((0.0, 0.0), "не должно"))
    req = DossierRequest(object_id="E1", parsed_query=ParsedQuery.model_validate({
        "household": [{"id": "kid", "label": "Ребёнок", "legs": [
            {"to_label": "Лицей 239", "to_kind": "school", "mode": "walk"}]}],
    }))
    data = mod._family_data(None, req, mod.ListingEvidence(37.6, 55.7, None, None, {}),
                            None, lambda _: (37.61, 55.71))
    assert data.members[0].legs[0].to_label == "Лицей 239"
    assert called == []
