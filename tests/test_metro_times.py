import json

import pytest

from habitus.geo.metro_times import (DEFAULT_TRANSFER_S, edge_seconds,
                                     headway_seconds, load_curated,
                                     transfer_seconds)


@pytest.fixture
def curated(tmp_path):
    (tmp_path / "metro").mkdir()
    (tmp_path / "metro" / "msk.json").write_text(json.dumps({
        "lines": [
            {"ref": "1", "system": "subway", "headway_s": 120,
             "fallback_speed_kmh": 40},
            {"ref": "D1", "system": "mcd", "headway_s": 600,
             "fallback_speed_kmh": 55},
        ],
        "edges": [
            {"line": "1", "from": "Сокольники", "to": "Красносельская",
             "seconds": 150},
        ],
        "transfers": [
            {"from": "Охотный Ряд", "to": "Театральная", "seconds": 180},
            {"from": "Площадь Гагарина", "to": "Ленинский проспект",
             "seconds": 420, "outdoor": True},
        ],
    }, ensure_ascii=False), encoding="utf-8")
    return load_curated("msk", tmp_path)


def test_headway_and_speed_come_from_data_not_code(curated):
    assert curated.headways["1"] == 120
    assert curated.headways["D1"] == 600
    assert curated.speeds["D1"] == 55.0


def test_curated_edge_is_used_verbatim(curated):
    seconds, estimated = edge_seconds(curated, "1", "Сокольники",
                                      "Красносельская", "subway", metres=1800)
    assert (seconds, estimated) == (150, False)


def test_curated_edge_matches_regardless_of_spelling(curated):
    # ё/е, регистр и лишние пробелы не должны рвать сопоставление
    seconds, estimated = edge_seconds(curated, "1", "СОКОЛЬНИКИ ",
                                      "красносельская", "subway", metres=1800)
    assert (seconds, estimated) == (150, False)


def test_missing_edge_falls_back_to_distance_and_is_marked(curated):
    # 2200 м на 40 км/ч = 198 с + стоянка; главное — пометка estimated
    seconds, estimated = edge_seconds(curated, "1", "Красносельская",
                                      "Комсомольская", "subway", metres=2200)
    assert estimated is True
    assert 180 < seconds < 260


def test_fallback_uses_the_lines_own_speed(curated):
    slow, _ = edge_seconds(curated, "1", "A", "B", "subway", metres=5000)
    fast, _ = edge_seconds(curated, "D1", "A", "B", "mcd", metres=5000)
    # у диаметров перегонная скорость выше — одна константа на всех врала бы
    assert fast < slow


def test_curated_transfer_carries_outdoor_flag(curated):
    seconds, estimated, outdoor = transfer_seconds(
        curated, "Площадь Гагарина", "Ленинский проспект")
    assert (seconds, estimated, outdoor) == (420, False, True)


def test_transfer_is_symmetric(curated):
    assert transfer_seconds(curated, "Театральная", "Охотный Ряд") == (180, False, False)


def test_unknown_transfer_falls_back_and_is_marked(curated):
    seconds, estimated, outdoor = transfer_seconds(curated, "A", "B")
    assert (seconds, estimated, outdoor) == (DEFAULT_TRANSFER_S, True, False)


def test_shipped_files_parse():
    # оба файла в репо должны читаться и содержать линии
    for city in ("msk", "spb"):
        c = load_curated(city)
        assert c.headways, f"{city}: интервалы не заданы"
        assert c.speeds, f"{city}: скорости фолбэка не заданы"


def test_shipped_msk_curated_pairs_actually_match():
    # опечатка в имени станции/линии в msk.json иначе тихо съедалась бы
    # фолбэком: estimated=True вместо ожидаемого estimated=False.
    msk = load_curated("msk")
    seconds, estimated = edge_seconds(msk, "1", "Сокольники",
                                      "Красносельская", "subway", metres=1800)
    assert (seconds, estimated) == (150, False)

    t_seconds, t_estimated, outdoor = transfer_seconds(
        msk, "Охотный Ряд", "Театральная")
    assert (t_seconds, t_estimated, outdoor) == (180, False, False)


def test_nonpositive_curated_edge_seconds_is_rejected(tmp_path):
    (tmp_path / "metro").mkdir()
    (tmp_path / "metro" / "msk.json").write_text(json.dumps({
        "lines": [{"ref": "1", "system": "subway", "headway_s": 120,
                   "fallback_speed_kmh": 40}],
        "edges": [{"line": "1", "from": "A", "to": "B", "seconds": 0}],
        "transfers": [],
    }, ensure_ascii=False), encoding="utf-8")
    with pytest.raises(ValueError):
        load_curated("msk", tmp_path)


def test_nonpositive_curated_transfer_seconds_is_rejected(tmp_path):
    (tmp_path / "metro").mkdir()
    (tmp_path / "metro" / "msk.json").write_text(json.dumps({
        "lines": [{"ref": "1", "system": "subway", "headway_s": 120,
                   "fallback_speed_kmh": 40}],
        "edges": [],
        "transfers": [{"from": "A", "to": "B", "seconds": -1}],
    }, ensure_ascii=False), encoding="utf-8")
    with pytest.raises(ValueError):
        load_curated("msk", tmp_path)


def test_headway_seconds_curated_hit(curated):
    seconds, estimated = headway_seconds(curated, "1", "subway")
    assert (seconds, estimated) == (120, False)


def test_headway_seconds_uncurated_line_falls_back_and_is_marked(curated):
    # линии "99" нет в курируемом файле фикстуры — обязан вернуться пессимистичный
    # дефолт по системе, а не 0 и не показатель курированной линии
    seconds, estimated = headway_seconds(curated, "99", "subway")
    assert (seconds, estimated) == (150, True)


def test_headway_seconds_uncurated_default_is_more_pessimistic_than_template(curated):
    # некурированная линия не должна тихо унаследовать шаблонное число, которое
    # выглядит как измеренное значение
    curated_seconds, _ = headway_seconds(curated, "1", "subway")
    uncurated_seconds, estimated = headway_seconds(curated, "99", "subway")
    assert estimated is True
    assert uncurated_seconds > curated_seconds


def test_nonpositive_curated_headway_is_rejected(tmp_path):
    (tmp_path / "metro").mkdir()
    (tmp_path / "metro" / "msk.json").write_text(json.dumps({
        "lines": [{"ref": "1", "system": "subway", "headway_s": 0,
                   "fallback_speed_kmh": 40}],
        "edges": [],
        "transfers": [],
    }, ensure_ascii=False), encoding="utf-8")
    with pytest.raises(ValueError):
        load_curated("msk", tmp_path)


def test_nonpositive_curated_fallback_speed_is_rejected(tmp_path):
    (tmp_path / "metro").mkdir()
    (tmp_path / "metro" / "msk.json").write_text(json.dumps({
        "lines": [{"ref": "1", "system": "subway", "headway_s": 120,
                   "fallback_speed_kmh": -5}],
        "edges": [],
        "transfers": [],
    }, ensure_ascii=False), encoding="utf-8")
    with pytest.raises(ValueError):
        load_curated("msk", tmp_path)

