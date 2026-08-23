from habitus.clean.normalize import CITY_BBOX, MSK_BBOX, is_valid


def _row(lon, lat, city=None):
    row = {"price": 10_000_000, "area": 50.0, "lat": lat, "lon": lon}
    if city is not None:
        row["city"] = city
    return row


def test_msk_bbox_alias_preserved():
    """Старое имя остаётся: на него ссылается код и тесты пайплайна."""
    assert MSK_BBOX == CITY_BBOX["msk"]


def test_row_without_city_is_validated_as_moscow():
    """Батч-пайплайн Циана не проставляет city в каждую строку — дефолт msk."""
    assert is_valid(_row(37.62, 55.75)) is True
    assert is_valid(_row(30.31, 59.94)) is False


def test_spb_coordinates_valid_for_spb_row():
    assert is_valid(_row(30.31, 59.94, city="spb")) is True


def test_moscow_coordinates_invalid_for_spb_row():
    """Координаты чужого города — отказ: это опечатка или подмена, а не объект."""
    assert is_valid(_row(37.62, 55.75, city="spb")) is False


def test_unknown_city_rejected():
    assert is_valid(_row(37.62, 55.75, city="dxb")) is False
