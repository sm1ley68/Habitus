# tests/test_area.py — область поиска: сторона города (округа) + именованное место
from habitus.online.geo import CARDINAL, AreaMatch, resolve_area


def test_cardinal_north_maps_to_three_okrugs():
    m = resolve_area("север")
    assert isinstance(m, AreaMatch)
    assert m.sql == "okrug = ANY(%s)"
    assert m.params == (["САО", "СВАО", "СЗАО"],)
    assert m.widen and m.widen[-1][0] == "TRUE"      # финальный шаг — снять область


def test_cardinal_diagonal_is_single_okrug():
    m = resolve_area("юго-запад москвы")
    assert m.params == (["ЮЗАО"],)


def test_center_maps_to_cao():
    m = resolve_area("в центре")
    assert m.params == (["ЦАО"],)
    assert "ЦАО" in m.label


def test_district_word_not_treated_as_cardinal_returns_none_without_conn():
    # «Северное Бутово» — не кардинал; без conn прочие ветки не отрабатывают → None
    assert resolve_area("Северное Бутово") is None


def test_empty_area_none():
    assert resolve_area("") is None
