"""Точки домохозяйства влияют на ПОРЯДОК выдачи, а не только на досье.

До этих тестов ParsedQuery.household доезжал исключительно до dossier.py:
состав семьи объяснял уже выбранный объект, но никак не участвовал в том,
какие объекты вообще попадут в shortlist, — то есть главный пункт УТП жил
только на экране досье.
"""
from datetime import datetime

import pytest

from habitus.config import settings
from habitus.online.household import (geocode_leg, household_points,
                                      total_metres)
from habitus.online.rerank import _household_norm, proximity_rerank
from habitus.online.retrieval import Candidate
from habitus.online.schema import ParsedQuery


def candidate(eid: str, lon: float | None, lat: float | None,
              score: float = 0.5) -> Candidate:
    return Candidate(external_id=eid, doc_text="", price=None, area=None,
                     rooms=None, facts={}, score=score,
                     updated_at=datetime(2026, 9, 1), lon=lon, lat=lat)


SCHOOL = (37.60, 55.75)
OFFICE = (37.54, 55.75)


def test_closer_to_named_places_ranks_higher():
    near = candidate("near", 37.57, 55.75)      # между школой и офисом
    far = candidate("far", 37.90, 55.75)        # далеко от обеих
    ranked = proximity_rerank(ParsedQuery(), [far, near],
                              household=[SCHOOL, OFFICE], top_n=2)
    assert [c.external_id for c in ranked] == ["near", "far"]


def test_signal_is_off_without_household():
    """Без семьи порядок не меняется вовсе — сигнал не должен подмешиваться
    в запросы, где домохозяйство не названо."""
    a, b = candidate("a", 37.90, 55.75, score=0.9), candidate("b", 37.57, 55.75, score=0.1)
    ranked = proximity_rerank(ParsedQuery(), [a, b], household=[], top_n=2)
    assert [c.external_id for c in ranked] == ["a", "b"]


def test_missing_coordinates_are_not_a_penalty_for_farness():
    """Объявление без geom в сигнале не участвует: это отсутствие данных, а
    не «далеко». Синтетического худшего расстояния мы не выдумываем."""
    norms = _household_norm([SCHOOL], [candidate("no-geom", None, None)])
    assert norms == [0.0]


def test_all_candidates_without_coordinates_give_no_signal():
    norms = _household_norm([SCHOOL, OFFICE],
                            [candidate("a", None, None), candidate("b", None, None)])
    assert norms == [0.0, 0.0]


def test_signal_cannot_outweigh_semantics_alone():
    """Вес домохозяйства — сигнал, а не фильтр: объект, выигравший семантику
    с большим отрывом, не должен проигрывать только из-за расположения."""
    strong_far = candidate("strong", 37.90, 55.75, score=1.0)
    weak_near = candidate("weak", 37.57, 55.75, score=0.0)
    ranked = proximity_rerank(ParsedQuery(), [strong_far, weak_near],
                              household=[SCHOOL, OFFICE], top_n=2)
    assert ranked[0].external_id == "strong"
    assert settings.household_weight < 1.0


def test_total_metres_sums_every_named_place():
    home = (37.57, 55.75)
    assert total_metres(home, [SCHOOL, OFFICE]) == pytest.approx(
        total_metres(home, [SCHOOL]) + total_metres(home, [OFFICE]))


# --- геокод меток: одно правило на поиск и на досье ------------------------

def leg(label: str, mode: str = "walk"):
    from habitus.online.schema import HouseholdLegIntent
    return HouseholdLegIntent(to_label=label, to_kind="work", mode=mode)


def test_city_suffix_added_unless_city_already_named():
    seen = []

    def geocoder(q):
        seen.append(q)
        return (37.6, 55.75)

    geocode_leg(leg("офис"), "msk", geocoder)
    geocode_leg(leg("Москва Сити"), "msk", geocoder)
    assert seen == ["офис, Москва", "Москва Сити"]


def test_geocode_outside_moscow_is_dropped_for_non_metro_legs():
    assert geocode_leg(leg("офис"), "msk", lambda _: (30.3, 59.9)) is None
    # у метро границей служит сам граф: МЦД уходят в область
    assert geocode_leg(leg("офис", "metro"), "msk", lambda _: (30.3, 59.9)) == (30.3, 59.9)


def test_unresolved_place_does_not_enter_the_signal():
    pq = ParsedQuery.model_validate({"household": [{
        "id": "parent", "label": "Родитель", "legs": [
            {"to_label": "Работа", "to_kind": "work", "mode": "walk"},
            {"to_label": "Небывалое место", "to_kind": "work", "mode": "walk"},
        ]}]})
    resolved = household_points(
        pq, "msk", lambda q: (37.6, 55.75) if q.startswith("Работа") else None)
    assert resolved == [(37.6, 55.75)]


def test_duplicate_places_counted_once():
    pq = ParsedQuery.model_validate({"household": [
        {"id": "mom", "label": "Мама", "legs": [
            {"to_label": "Офис", "to_kind": "work", "mode": "walk"}]},
        {"id": "dad", "label": "Папа", "legs": [
            {"to_label": "Офис", "to_kind": "work", "mode": "walk"}]},
    ]})
    assert household_points(pq, "msk", lambda _: (37.6, 55.75)) == [(37.6, 55.75)]


def test_living_at_one_office_loses_to_living_between_two():
    """Ключевое свойство метрики: на отрезке между двумя офисами СУММА
    расстояний постоянна, поэтому по сумме «жить вплотную к одному офису, а
    второму ездить через весь город» неотличимо от «жить посередине». Семья,
    которая просит компромисс, просит ровно обратного — поэтому
    household_cost добавляет к среднему худшее плечо.
    """
    at_office = candidate("at-office", 37.50, 55.75)
    in_between = candidate("between", 37.60, 55.75)
    points = [(37.50, 55.75), (37.70, 55.75)]

    # сумма их не различает…
    assert total_metres((37.50, 55.75), points) == pytest.approx(
        total_metres((37.60, 55.75), points), rel=1e-3)
    # …а цена расположения — различает
    from habitus.online.household import household_cost
    assert household_cost((37.60, 55.75), points) < household_cost((37.50, 55.75), points)

    ranked = proximity_rerank(ParsedQuery(), [at_office, in_between],
                              household=points, top_n=2)
    assert [c.external_id for c in ranked] == ["between", "at-office"]


def test_household_cost_of_no_points_is_not_zero_distance():
    """Пустой список — отсутствие сигнала, а не «идеально близко». Проверяем,
    что вызывающий обязан различать это сам: _household_norm на пустых точках
    возвращает нули, а не единицы."""
    from habitus.online.household import household_cost
    assert household_cost((37.6, 55.75), []) == 0.0
    assert _household_norm([], [candidate("a", 37.6, 55.75)]) == [0.0]
