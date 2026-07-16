import pytest

from habitus.geo.evidence import EvidenceValidationError, validate_feature


def feature(layer="crime", geometry_type="Polygon", **props):
    properties = {
        "source_id": "1", "source": "fixture", "city": "msk",
        "layer": layer, "observed_at": "2026-07-16T00:00:00Z",
        **props,
    }
    coordinates = [[[37.6, 55.7], [37.61, 55.7], [37.61, 55.71], [37.6, 55.7]]]
    if geometry_type == "Point":
        coordinates = [37.6, 55.7]
    return {"type": "Feature", "properties": properties,
            "geometry": {"type": geometry_type, "coordinates": coordinates}}


def test_risk_feature_requires_normalized_polygon():
    row = validate_feature(feature(weight=.42))
    assert row["layer"] == "crime" and row["weight"] == .42 and row["db"] is None
    with pytest.raises(EvidenceValidationError):
        validate_feature(feature(weight=1.2))
    with pytest.raises(EvidenceValidationError):
        validate_feature(feature(geometry_type="Point", weight=.2))


def test_noise_feature_requires_db_and_moscow():
    row = validate_feature(feature("noise", "Point", db=48.5))
    assert row["db"] == 48.5 and row["weight"] is None
    with pytest.raises(EvidenceValidationError):
        validate_feature(feature("noise", "Point"))
    with pytest.raises(EvidenceValidationError):
        validate_feature(feature(weight=.2, city="spb"))


def test_coordinates_are_strict_lng_lat_epsg4326():
    bad = feature(weight=.2)
    bad["geometry"]["coordinates"][0][0] = [200, 55.7]
    with pytest.raises(EvidenceValidationError, match="EPSG:4326"):
        validate_feature(bad)

    not_a_pair = feature(weight=.2)
    not_a_pair["geometry"]["coordinates"][0][0] = [37.6, 55.7, 10]
    with pytest.raises(EvidenceValidationError, match=r"\[lng, lat\]"):
        validate_feature(not_a_pair)


def test_evidence_source_and_timestamp_are_strict():
    with pytest.raises(EvidenceValidationError, match="source must"):
        validate_feature(feature(weight=.2, source=7))
    with pytest.raises(EvidenceValidationError, match="timezone"):
        validate_feature(feature(weight=.2, observed_at="2026-07-16T00:00:00"))
