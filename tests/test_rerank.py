from dataclasses import replace
from datetime import datetime, timezone
import pytest
from habitus.config import settings
from habitus.online.rerank import prefilter_pool, proximity_rerank, rerank
from habitus.online.retrieval import Candidate
from habitus.online.schema import GeoConstraint, ParsedQuery


def _cand(eid: str, doc: str, facts: dict | None = None) -> Candidate:
    return Candidate(external_id=eid, doc_text=doc, price=None, area=None,
                     rooms=None, facts=facts or {}, score=0.0,
                     updated_at=datetime(2026, 7, 1, tzinfo=timezone.utc))


class FakeReranker:
    """Скорит по вхождению слова «школа» — детерминированно, без модели."""
    def __init__(self):
        self.pairs = None

    def compute_score(self, pairs, normalize=True, max_length=None):
        self.pairs = pairs
        return [0.9 if "школа" in doc else 0.1 for _, doc in pairs]


def test_rerank_orders_and_cuts_top_n():
    cands = [_cand("A", "просто квартира"), _cand("B", "школа рядом"),
             _cand("C", "ещё вариант")]
    fr = FakeReranker()
    out = rerank("школа в 10 минутах", cands, top_n=2, reranker=fr)
    assert [c.external_id for c in out] == ["B", "A"]   # tie A/C — стабильный порядок
    assert out[0].score == 0.9 and out[1].score == 0.1
    # пары (запрос, doc_text) ушли в реранкер
    assert fr.pairs[0] == ["школа в 10 минутах", "просто квартира"]


def test_rerank_single_candidate_scalar_score():
    class ScalarReranker:
        def compute_score(self, pairs, normalize=True, max_length=None):
            return 0.42          # FlagReranker для одной пары возвращает скаляр
    out = rerank("q", [_cand("A", "doc")], reranker=ScalarReranker())
    assert len(out) == 1 and out[0].score == 0.42


def test_rerank_empty_input():
    assert rerank("q", [], reranker=None) == []


def _geo_pq() -> ParsedQuery:
    return ParsedQuery.model_validate({"geo": [{"kind": "metro", "walk_minutes": 10}]})


def test_prefilter_pool_no_geo_truncates_by_rrf_order():
    cands = [_cand(str(i), f"doc{i}") for i in range(50)]
    out = prefilter_pool(ParsedQuery(semantic_text="q"), cands, pool_n=10)
    assert [c.external_id for c in out] == [str(i) for i in range(10)]


def test_prefilter_pool_geo_pulls_close_tail_candidate_keeps_rrf_head():
    # 50 кандидатов в RRF-порядке; только у "49" (хвост, за pool_n=10) есть
    # структурная близость — должен попасть в пул, не вытеснив голову RRF.
    cands = [_cand(str(i), f"doc{i}") for i in range(50)]
    cands[49] = _cand("49", "doc49", facts={"walk_min_metro": 1})
    out = prefilter_pool(_geo_pq(), cands, pool_n=10)
    ids = [c.external_id for c in out]
    assert ids[:5] == ["0", "1", "2", "3", "4"]   # RRF-голова (ceil(10/2)=5) сохранена
    assert "49" in ids


def test_prefilter_pool_missing_geo_data_does_not_crash_or_crowd_out():
    # 20 кандидатов без walk_min_*, кроме "15" (хвост, за pool_n=6) — с данными.
    pq = _geo_pq()
    cands = [_cand(str(i), f"doc{i}") for i in range(20)]
    cands[15] = _cand("15", "doc15", facts={"walk_min_metro": 2})
    out = prefilter_pool(pq, cands, pool_n=6)
    ids = [c.external_id for c in out]
    assert ids[:3] == ["0", "1", "2"]             # RRF-голова (ceil(6/2)=3) сохранена
    assert "15" in ids                            # единственный с данными — попал в пул


# --- household-голова в пуле ------------------------------------------------
#
# Канал retrieval честно доставал эталон d-серии в top-100, а срез пула его
# выбрасывал: у запросов «я в Сити, жена у Курского» гео-оси в смысле walk_min
# нет, и пул сводился к первым pool_n RRF. До кросс-энкодера доезжали 2 объекта
# из 10 (docs/notes/eval-baseline-2026-09-04.md).

def _at(eid: str, lon: float, lat: float) -> Candidate:
    c = _cand(eid, f"doc{eid}")
    return replace(c, lon=lon, lat=lat)


def test_prefilter_pool_household_pulls_close_tail_candidate():
    # 50 кандидатов в RRF-порядке, все далеко от точек семьи, кроме "49" —
    # он в хвосте, за pool_n=10, и по RRF в пул не попал бы.
    cands = [_at(str(i), 37.90, 55.90) for i in range(50)]
    cands[49] = _at("49", 37.60, 55.75)
    out = prefilter_pool(ParsedQuery(semantic_text="q"), cands, pool_n=10,
                         household=[(37.60, 55.75), (37.62, 55.76)])
    ids = [c.external_id for c in out]
    assert ids[:5] == ["0", "1", "2", "3", "4"]   # голова RRF (ceil(10/2)=5) на месте
    assert "49" in ids


def test_prefilter_pool_household_ignores_candidates_without_coordinates():
    # Объект без geom в household-сигнале не участвует: подставлять ему
    # фиктивный «худший» скор значит гадать, а не мерить.
    cands = [_at(str(i), 37.90, 55.90) for i in range(20)]
    cands[15] = _cand("15", "doc15")               # lon/lat = None
    out = prefilter_pool(ParsedQuery(semantic_text="q"), cands, pool_n=6,
                         household=[(37.60, 55.75)])
    ids = [c.external_id for c in out]
    assert ids[:3] == ["0", "1", "2"]              # голова RRF (ceil(6/2)=3)
    # "15" остался за пулом: в household-голову он не попал (координат нет), а
    # добор хвостом до него не дошёл — фиктивной «худшей» близости ему никто
    # не приписал.
    assert "15" not in ids


def test_prefilter_pool_two_axes_split_pool_in_thirds():
    # Названы обе оси — ни одна не вытесняет голову RRF: 1/3 пула каждой.
    cands = [_at(str(i), 37.90, 55.90) for i in range(60)]
    cands[58] = replace(_at("58", 37.90, 55.90), facts={"walk_min_metro": 1})
    cands[59] = _at("59", 37.60, 55.75)
    out = prefilter_pool(_geo_pq(), cands, pool_n=9,
                         household=[(37.60, 55.75)])
    ids = [c.external_id for c in out]
    assert ids[:3] == ["0", "1", "2"]              # ceil(9/3)=3
    assert "58" in ids and "59" in ids


def test_prefilter_pool_without_household_keeps_previous_behaviour():
    # Регресс-страховка: без точек семьи срез обязан остаться прежним.
    cands = [_at(str(i), 37.90, 55.90) for i in range(50)]
    out = prefilter_pool(ParsedQuery(semantic_text="q"), cands, pool_n=10)
    assert [c.external_id for c in out] == [str(i) for i in range(10)]


def test_prefilter_pool_short_input_returned_as_is():
    cands = [_cand(str(i), f"doc{i}") for i in range(5)]
    out = prefilter_pool(_geo_pq(), cands, pool_n=10)
    assert out is cands


def test_prefilter_pool_tie_break_by_external_id_not_input_order():
    # Тай-брейк по external_id должен решать порядок сам по себе, а не
    # "чей случайно вход был отсортирован" — иначе прогон детерминирован
    # только потому, что вход не менялся между вызовами, а не потому, что
    # есть явный тай-брейк.
    head = [_cand(f"H{i}", f"headdoc{i}") for i in range(3)]     # без близости
    # у всех одинаковый _proximity_raw (тай), поданы НЕ по алфавиту id —
    # без тай-брейка sorted() сохранил бы именно этот (неалфавитный) порядок
    tied_ids = ["Tg", "Te", "Ta", "Tf", "Tc", "Tb", "Td"]
    tied = [_cand(tid, f"doc-{tid}", facts={"walk_min_metro": 5}) for tid in tied_ids]
    out = prefilter_pool(_geo_pq(), head + tied, pool_n=6)
    ids = [c.external_id for c in out]
    assert ids[:3] == ["H0", "H1", "H2"]      # RRF-голова
    assert ids[3:] == ["Ta", "Tb", "Tc"]      # proximity-голова: тай-брейк по id


def test_prefilter_pool_geo_reaches_pool_n_when_prox_order_matches_rrf():
    # Ревьюер code review: если proximity-порядок совпадает с RRF-порядком
    # (частый случай — гео-ось уже отфильтровала retrieval по walk_min),
    # пересечение голов схлопывается почти полностью; без добора хвостом пул
    # получался вдвое меньше pool_n (20 вместо 40 на этом самом кейсе).
    cands = [_cand(str(i), f"doc{i}", facts={"walk_min_metro": i})
             for i in range(100)]
    out = prefilter_pool(_geo_pq(), cands, pool_n=40)
    assert len(out) == 40


def test_prefilter_pool_geo_reaches_pool_n_with_sparse_proximity_data():
    # Второй замер ревьюера: данные по близости есть лишь у 5 из 100 —
    # без добора пул получался 25 вместо 40.
    cands = [_cand(str(i), f"doc{i}") for i in range(100)]
    for i in (60, 70, 80, 90, 95):
        cands[i] = _cand(str(i), f"doc{i}", facts={"walk_min_metro": 1})
    out = prefilter_pool(_geo_pq(), cands, pool_n=40)
    assert len(out) == 40


def test_proximity_rerank_orientation_match_lifts_equal_candidate():
    # ориентация — не фильтр, а бонус в финальном бленде (settings.orientation_
    # weight): при прочих равных совпадение поднимает кандидата выше
    pq = ParsedQuery(window_orientation=["SW"])
    cands = [_cand("A", "doc a", facts={"window_orientation": ["N"]}),
             _cand("B", "doc b", facts={"window_orientation": ["SW"]})]
    out = proximity_rerank(pq, cands)
    assert [c.external_id for c in out] == ["B", "A"]


def test_proximity_rerank_missing_orientation_not_ranked_below_mismatch():
    # отсутствие данных об ориентации — не штраф (0), как и несовпадение (тоже
    # 0) — кандидат без данных не должен провалиться ниже несовпавшего.
    # Кандидат без данных идёт ПЕРВЫМ во входе: при штрафе за отсутствие данных
    # он бы уехал вниз и порядок сломался, а тай-брейк по входному индексу
    # такой ошибки не замаскирует.
    pq = ParsedQuery(window_orientation=["SW"])
    cands = [_cand("A", "doc a"),                          # данных нет вовсе
             _cand("B", "doc b", facts={"window_orientation": ["N"]})]
    out = proximity_rerank(pq, cands)
    assert [c.external_id for c in out] == ["A", "B"]
    assert out[0].score == out[1].score      # оба ровно 0 — ни штрафа, ни бонуса


def test_proximity_rerank_orientation_case_insensitive():
    # NLU и clean/windows.py сегодня дают верхний регистр, но сравнение не
    # должно зависеть от этого совпадения
    pq = ParsedQuery(window_orientation=["sw"])
    cands = [_cand("A", "doc a", facts={"window_orientation": ["N"]}),
             _cand("B", "doc b", facts={"window_orientation": ["Sw"]})]
    out = proximity_rerank(pq, cands)
    assert [c.external_id for c in out] == ["B", "A"]


def test_proximity_rerank_orientation_bonus_applies_on_geo_branch():
    # бонус живёт в обеих ветках бленда: с гео-осью он тоже слагаемое, а не
    # только в ветке «гео нет»
    pq = ParsedQuery(window_orientation=["SW"],
                     geo=[GeoConstraint(kind="metro", walk_minutes=10)])
    cands = [_cand("A", "doc a", facts={"walk_min_metro": 5,
                                        "window_orientation": ["N"]}),
             _cand("B", "doc b", facts={"walk_min_metro": 5,
                                        "window_orientation": ["SW"]})]
    out = proximity_rerank(pq, cands)
    assert [c.external_id for c in out] == ["B", "A"]
    assert out[0].score - out[1].score == pytest.approx(settings.orientation_weight)


def test_proximity_rerank_keeps_input_order_on_ties_with_orientation():
    # деградированный путь filter_only_search: все score равны, порядок задан
    # свежестью — бонус ориентации не должен подменять его алфавитом id
    pq = ParsedQuery(window_orientation=["SW"])
    cands = [_cand("Z", "doc z"), _cand("A", "doc a")]
    out = proximity_rerank(pq, cands)
    assert [c.external_id for c in out] == ["Z", "A"]
