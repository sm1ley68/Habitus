import pytest
from pydantic import ValidationError
from habitus.online.schema import (GeoConstraint, ParsedQuery, PointConstraint,
                                   ResultItem, SearchRequest, SearchResponse)


def test_parsed_query_defaults():
    pq = ParsedQuery()
    assert pq.price_min is None and pq.price_max is None
    assert pq.geo == [] and pq.window_orientation == [] and pq.stop_factors == []
    assert pq.semantic_text == "" and pq.lang == "ru"


def test_parsed_query_full():
    pq = ParsedQuery(price_max=15_000_000, rooms=[1, 2],
                     geo=[GeoConstraint(kind="school", walk_minutes=10)],
                     noise_max="low", stop_factors=["bars"],
                     semantic_text="двор-колодец", lang="ru")
    assert pq.geo[0].kind == "school" and pq.rooms == [1, 2]


def test_parsed_query_rejects_bad_enum():
    with pytest.raises(ValidationError):
        ParsedQuery(noise_max="loud")
    with pytest.raises(ValidationError):
        GeoConstraint(kind="shop", walk_minutes=5)


def test_search_response_roundtrip():
    resp = SearchResponse(
        results=[ResultItem(external_id="E1", price=10_000_000, area=45.0,
                            rooms=2, address_facts={"noise_level": "low"}, score=0.9)],
        explanation="тихо и школа рядом", parsed=ParsedQuery(),
        data_freshness="данные актуальны на 2026-07-11 10:00")
    again = SearchResponse.model_validate(resp.model_dump())
    assert again.results[0].external_id == "E1"
    assert again.relaxed == [] and again.degraded == []


def test_search_request_requires_query():
    with pytest.raises(ValidationError):
        SearchRequest(query="")
    req = SearchRequest(query="тихо", point=PointConstraint(lon=37.6, lat=55.7))
    assert req.point.minutes == 15 and req.point.mode == "foot-walking"
