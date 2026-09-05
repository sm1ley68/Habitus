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
