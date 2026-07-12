from habitus.embed.document import build_doc_text, content_hash


def test_build_doc_text_mute_row():
    row = {"rooms": 2, "area": 54.0, "level": 7, "levels": 12,
           "window_orientation": None, "walk_min_school": 11.5,
           "walk_min_metro": 4.0, "walk_min_park": 6.0,
           "bar_density_500m": 0, "noise_level": "low", "description": None}
    text = build_doc_text(row)
    assert "2-комн" in text
    assert "54" in text
    assert "7/12" in text
    assert "школ" in text.lower()
    assert "метро в 4 мин" in text
    assert "парк в 6 мин" in text
    assert "баров" in text.lower() or "бар" in text.lower()


def test_build_doc_text_omits_missing_geo():
    # метро/парк не заданы — в текст не попадают, строка не ломается
    row = {"rooms": 1, "area": 40.0, "walk_min_school": 5.0,
           "walk_min_metro": None, "walk_min_park": None, "description": None}
    text = build_doc_text(row)
    assert "метро" not in text and "парк" not in text
    assert "школа в 5 мин" in text


def test_build_doc_text_prepends_real_description():
    row = {"rooms": 1, "area": 33.0, "level": 3, "levels": 5,
           "description": "Уютная квартира, тихий двор-колодец",
           "walk_min_school": None, "bar_density_500m": 5, "noise_level": "high"}
    text = build_doc_text(row)
    assert "двор-колодец" in text
    assert "1-комн" in text


def test_content_hash_stable():
    assert content_hash("abc") == content_hash("abc")
    assert content_hash("abc") != content_hash("abd")
