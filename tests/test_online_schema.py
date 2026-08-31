import pytest
from pydantic import ValidationError
from habitus.online.schema import (GeoConstraint, HouseholdLegIntent,
                                   LineStringGeometry, MetroRide, MetroSegment,
                                   MetroTransfer, ParsedQuery, ParsedTurn,
                                   PointConstraint, ResultItem, RouteLeg,
                                   SearchRequest, SearchResponse)


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


def test_search_request_query_max_length():
    with pytest.raises(ValidationError):
        SearchRequest(query="а" * 2001)
    req = SearchRequest(query="а" * 2000)   # ровно граница — валидно
    assert len(req.query) == 2000


def test_point_constraint_rejects_out_of_range_lon_lat():
    with pytest.raises(ValidationError):
        PointConstraint(lon=200, lat=55)
    with pytest.raises(ValidationError):
        PointConstraint(lon=37.6, lat=100)


def test_point_constraint_rejects_bad_minutes():
    with pytest.raises(ValidationError):
        PointConstraint(lon=37.6, lat=55.7, minutes=0)
    with pytest.raises(ValidationError):
        PointConstraint(lon=37.6, lat=55.7, minutes=61)


def test_point_constraint_rejects_unknown_mode():
    with pytest.raises(ValidationError):
        PointConstraint(lon=37.6, lat=55.7, mode="rocket")


def test_point_constraint_valid_defaults():
    pc = PointConstraint(lon=37.6, lat=55.7)
    assert pc.mode == "foot-walking" and pc.minutes == 15


def test_household_leg_requires_explicit_valid_mode_and_time():
    with pytest.raises(ValidationError):
        HouseholdLegIntent(to_label="Школа", to_kind="school")
    with pytest.raises(ValidationError):
        HouseholdLegIntent(to_label="Школа", to_kind="school", mode="taxi")
    with pytest.raises(ValidationError):
        HouseholdLegIntent(to_label="Школа", to_kind="school",
                           mode="walk", depart="25:00")


def test_linestring_coordinates_are_lng_lat():
    geometry = LineStringGeometry(coordinates=[(37.6, 55.7), (37.7, 55.8)])
    assert geometry.coordinates[0] == (37.6, 55.7)
    with pytest.raises(ValidationError):
        LineStringGeometry(coordinates=[(55.7, 200), (37.7, 55.8)])


def test_parsed_turn_defaults():
    turn = ParsedTurn()
    assert turn.intent == "new_search"
    assert turn.query == ParsedQuery()
    assert turn.cleared_fields == []


def test_parsed_turn_drops_unknown_cleared_field_names():
    # неизвестное имя поля от LLM не роняет разбор — молча отбрасывается
    turn = ParsedTurn(intent="refine", cleared_fields=["noise_max", "bogus_field"])
    assert turn.cleared_fields == ["noise_max"]


def test_search_request_accepts_prev_parsed():
    req = SearchRequest(query="подешевле",
                        prev_parsed=ParsedQuery(price_max=20_000_000))
    assert req.prev_parsed.price_max == 20_000_000


def test_search_request_prev_parsed_defaults_to_none():
    req = SearchRequest(query="тихо")
    assert req.prev_parsed is None


def test_search_response_intent_defaults_to_new_search():
    resp = SearchResponse(results=[], explanation="", parsed=ParsedQuery(),
                          data_freshness="нет данных")
    assert resp.intent == "new_search"


def test_search_request_top_n_bounds():
    # запас выдачи ограничен схемой: 0 и 51 не должны доезжать до пайплайна
    assert SearchRequest(query="q", top_n=1).top_n == 1
    assert SearchRequest(query="q", top_n=50).top_n == 50
    assert SearchRequest(query="q").top_n is None      # дефолт — result_max_n
    for bad in (0, -1, 51):
        with pytest.raises(ValidationError):
            SearchRequest(query="q", top_n=bad)


def _ride(**over):
    base = dict(
        walk_from_home_min=7, walk_to_dest_min=5,
        segments=[MetroSegment(line_ref="1", line_name="Сокольническая",
                               system="subway", colour="#EF161E",
                               from_station="Сокольники", to_station="Охотный Ряд",
                               stops=6, minutes=13)],
        transfers=[], total_minutes=25)
    base.update(over)
    return MetroRide(**base)


def test_point_constraint_accepts_metro_mode():
    assert PointConstraint(lon=37.6, lat=55.75, minutes=40, mode="metro").mode == "metro"


def test_point_constraint_still_rejects_unknown_mode_after_metro_added():
    with pytest.raises(ValidationError):
        PointConstraint(lon=37.6, lat=55.75, minutes=40, mode="teleport")


def test_route_leg_metro_is_optional():
    leg = RouteLeg(to_label="офис", to_kind="work", mode="walk",
                   depart="08:00", arrive="08:30", minutes=30, safety="safe",
                   geometry=LineStringGeometry(coordinates=[(37.6, 55.7), (37.61, 55.71)]))
    assert leg.metro is None


def test_segment_rejects_unknown_system():
    with pytest.raises(ValidationError):
        MetroSegment(line_ref="1", line_name="л", system="tram", colour=None,
                     from_station="A", to_station="B", stops=2, minutes=5)


def test_estimated_defaults_to_false_everywhere():
    ride = _ride()
    assert ride.estimated is False
    assert ride.segments[0].estimated is False
    transfer = MetroTransfer(from_station="A", to_station="B", minutes=3)
    assert transfer.outdoor is False
    assert transfer.estimated is False


def test_metro_ride_field_sets_are_exact():
    # Ловит незамеченный rename поля: Task 14/16 диффятся по этой форме
    # field-for-field, а без extra="forbid" переименование не бьёт по тестам,
    # если сами модели не утверждают точный набор полей.
    assert set(MetroSegment.model_fields) == {
        "line_ref", "line_name", "system", "colour", "from_station",
        "to_station", "stops", "minutes", "estimated",
    }
    assert set(MetroTransfer.model_fields) == {
        "from_station", "to_station", "minutes", "outdoor", "estimated",
    }
    assert set(MetroRide.model_fields) == {
        "walk_from_home_min", "walk_to_dest_min", "segments", "transfers",
        "total_minutes", "wait_min", "estimated",
    }


def test_segment_accepts_mck_and_mcd_systems():
    for system in ("mck", "mcd"):
        seg = MetroSegment(line_ref="14", line_name="МЦК", system=system,
                           colour=None, from_station="A", to_station="B",
                           stops=2, minutes=5)
        assert seg.system == system


def test_segment_colour_accepts_css_name_not_only_hex():
    # МЦК приходит из OSM как CSS-имя "red", не "#rrggbb" — валидатор формата
    # цвета не должен был бы появиться здесь, эта проба это утверждает
    seg = MetroSegment(line_ref="14", line_name="МЦК", system="mck",
                       colour="red", from_station="A", to_station="B",
                       stops=2, minutes=5)
    assert seg.colour == "red"


def test_ride_total_is_the_door_to_door_number():
    # RouteLeg.minutes — итог, MetroRide — его разбивка; фронт не складывает заново
    ride = _ride()
    leg = RouteLeg(to_label="офис", to_kind="work", mode="metro",
                   depart="08:00", arrive="08:25", minutes=ride.total_minutes,
                   safety="safe",
                   geometry=LineStringGeometry(coordinates=[(37.6, 55.7), (37.61, 55.71)]),
                   metro=ride)
    assert leg.minutes == leg.metro.total_minutes == 25
