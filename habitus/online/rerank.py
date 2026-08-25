# habitus/online/rerank.py — bge-reranker-v2-m3, ленивая загрузка (как get_model в embed)
import math
from dataclasses import replace

from habitus.config import settings
from habitus.embed.encode import RERANK_LOCK
from habitus.online.retrieval import Candidate
from habitus.online.schema import ParsedQuery

_reranker = None
_cuda: bool | None = None


def _cuda_available() -> bool:
    """Есть ли CUDA. Результат кэшируется: torch импортируется лениво, а на
    горячем пути реранка проверка зовётся на каждый запрос."""
    global _cuda
    if _cuda is None:
        import torch
        _cuda = torch.cuda.is_available()
    return _cuda


def _pool_configured() -> bool:
    """Задавал ли оператор RERANK_POOL_N явно (env или .env)."""
    return "rerank_pool_n" in settings.model_fields_set


def effective_pool_n(pool_n: int | None = None) -> int:
    """Сколько пар реально отдать кросс-энкодеру.

    Вне CUDA реранкер идёт на CPU (см. get_reranker), а он линеен по числу пар:
    на слабой машине пул в 40 пар — это десятки секунд, шлюз рвёт запрос по
    таймауту и показывает ошибку, хотя ML честно досчитывает позже. Поэтому
    дефолтный пул вне CUDA режется до rerank_pool_n_cpu.

    Потолок — это дефолт для того, кто ничего не настраивал, а не запрет.
    Явный запрос уважается в двух видах, и оба важны:
      * аргумент pool_n — так зовут из тестов и из кода, который знает, чего хочет;
      * заданный оператором RERANK_POOL_N — иначе `RERANK_POOL_N=100 habitus eval`
        молча мерил бы 25 и врал бы в замерах.
    """
    if pool_n is not None:
        return pool_n
    if _pool_configured():
        return settings.rerank_pool_n
    if _cuda_available():
        return settings.rerank_pool_n
    return min(settings.rerank_pool_n, settings.rerank_pool_n_cpu)


def get_reranker():
    global _reranker
    if _reranker is None:
        import torch
        from FlagEmbedding import FlagReranker
        # На Apple MPS кросс-энкодер реранкера непригоден: с fp16 роняет forward на
        # длинных документах, а без fp16 — зависает на MPS-устройстве (замерено:
        # 50 пар > 10 мин против 176 c на CPU). Поэтому вне CUDA пинуем на CPU.
        # fp16 стабилен и выгоден только на CUDA. В проде (Linux/CPU) — тоже CPU.
        cuda = torch.cuda.is_available()
        _reranker = FlagReranker(settings.reranker_model, use_fp16=cuda,
                                 devices=None if cuda else "cpu")
    return _reranker


def rerank(query: str, candidates: list[Candidate], top_n: int | None = None,
           reranker=None) -> list[Candidate]:
    """(запрос, doc_text) пары → скоры реранкера → top-N по убыванию."""
    if not candidates:
        return []
    n = top_n or settings.rerank_top_n
    r = reranker or get_reranker()
    with RERANK_LOCK:
        scores = r.compute_score([[query, c.doc_text] for c in candidates],
                                 normalize=True,
                                 max_length=settings.rerank_max_length)
    if not isinstance(scores, list):        # одна пара → скаляр
        scores = [scores]
    ranked = sorted(zip(candidates, scores), key=lambda p: -p[1])
    return [replace(c, score=float(s)) for c, s in ranked[:n]]


def _minmax(values: list[float]) -> list[float]:
    """min-max в [0,1]. Вырожденный диапазон (все равны) → константа 0.0
    (не влияет на порядок — решает другой сигнал бленда)."""
    lo, hi = min(values), max(values)
    if hi == lo:
        return [0.0] * len(values)
    return [(v - lo) / (hi - lo) for v in values]


def _proximity_raw(pq: ParsedQuery, c: Candidate) -> float | None:
    """Composite близости = сумма walk_min по ОСЯМ, которые запрос явно попросил
    (как order_sql в build_golden). None, если хоть по одной оси нет данных."""
    vals = [c.facts.get(f"walk_min_{g.kind}") for g in pq.geo]
    if not vals or any(v is None for v in vals):
        return None
    return float(sum(vals))


def _orientation_bonus(pq: ParsedQuery, candidates: list[Candidate]) -> list[float]:
    """Бонус settings.orientation_weight за совпадение ориентации окон — сигнал в
    финальном бленде, не фильтр (данные есть у ~2% объявлений). Срабатывает
    только когда pq.window_orientation непуст. Отсутствие данных об ориентации
    у кандидата — не штраф и не бонус, а 0: неизвестно, не «плохая ориентация»."""
    if not pq.window_orientation:
        return [0.0] * len(candidates)
    # верхний регистр с обеих сторон: то же сравнение в dossier.py приводит
    # значения из БД к .upper(), и расходиться этим двум местам незачем
    requested = {d.upper() for d in pq.window_orientation}
    bonus = settings.orientation_weight
    return [bonus if requested & {d.upper() for d in
                                  (c.facts.get("window_orientation") or ())}
            else 0.0 for c in candidates]


def prefilter_pool(pq: ParsedQuery, candidates: list[Candidate],
                   pool_n: int | None = None) -> list[Candidate]:
    """Сузить кандидатов до пула, который увидит кросс-энкодер (settings.rerank_pool_n).

    Без гео-оси в запросе близость мерить нечем — берём голову RRF-порядка как есть.
    С гео-осью пул — объединение двух голов по ceil(pool_n/2): голова RRF (не
    прогадать с семантикой/лексикой) + голова по возрастанию composite-близости
    walk_min_* (не прогадать со структурно близкими, которые RRF мог утопить в
    хвосте). Кандидаты без данных по хотя бы одной запрошенной оси в
    proximity-голову не попадают — вставлять их туда через фиктивный «худший»
    скор значит гадать, а не мерить. Если объединение голов не дотягивает до
    pool_n (головы сильно пересекаются — например, proximity-порядок совпал с
    RRF-порядком, что и есть частый случай: гео-ось уже отфильтровала retrieval
    по walk_min, и близость коррелирует с RRF), пул добирается хвостом из
    остатка candidates в исходном RRF-порядке — иначе кросс-энкодер получает
    меньше пар, чем обещает settings.rerank_pool_n, ровно на тех запросах, ради
    которых вторая голова и вводилась.
    """
    n = effective_pool_n(pool_n)
    if len(candidates) <= n:
        return candidates
    if not pq.geo:
        return candidates[:n]

    head_n = math.ceil(n / 2)
    rrf_head = candidates[:head_n]
    prox_ranked = sorted(
        ((c, r) for c in candidates if (r := _proximity_raw(pq, c)) is not None),
        key=lambda cr: (cr[1], cr[0].external_id))
    prox_head = [c for c, _ in prox_ranked[:head_n]]

    seen = {c.external_id for c in rrf_head}
    pool = list(rrf_head)
    for c in prox_head:
        if c.external_id not in seen:
            seen.add(c.external_id)
            pool.append(c)
    if len(pool) < n:
        for c in candidates:
            if len(pool) >= n:
                break
            if c.external_id not in seen:
                seen.add(c.external_id)
                pool.append(c)
    return pool[:n]


def proximity_rerank(pq: ParsedQuery, candidates: list[Candidate], *,
                     weight: float | None = None,
                     top_n: int | None = None) -> list[Candidate]:
    """Блендинг структурного сигнала точной близости с семантическим score.

    Cross-encoder-реранкер слеп к точным минутам (в doc_text они — крошечный хвост
    на фоне ~1600 символов прозы). Здесь среди уже отфильтрованных и семантически
    отранжированных кандидатов подмешиваем нормированную близость по осям запроса:
        blended = weight * proximity_norm + (1 - weight) * score_norm
    weight — доля близости (`settings.proximity_weight`). Это бленд, а не сортировка
    по оси: семантика сохраняет вес, поэтому метрика меряет реальное улучшение
    ранжирования, а не тавтологию «отсортировали ровно по тому, чем метили golden».

    Поверх этого бленда, независимо от гео-оси, добавляется бонус
    `settings.orientation_weight` за совпадение ориентации окон
    (`_orientation_bonus`) — тоже сигнал, не фильтр: window_orientation не режет
    выборку в build_where, потому что данные есть у ~2% объявлений.
    """
    if not candidates:
        return []
    n = top_n or settings.rerank_top_n
    w = settings.proximity_weight if weight is None else weight
    orient_bonus = _orientation_bonus(pq, candidates)

    # нет оси близости в запросе или нулевой вес → близость не при чём. Если
    # вдобавок нет и запроса на ориентацию — сохраняем входной порядок
    # (RRF / реранкер / свежесть), только срез top-N; иначе поверх семантики
    # подмешивается только бонус ориентации.
    if not pq.geo or w <= 0.0:
        if not pq.window_orientation:
            return candidates[:n]
        score_norm = _minmax([c.score for c in candidates])
        blended = [s + o for s, o in zip(score_norm, orient_bonus)]
        # тай-брейк по входному индексу, а не по external_id: на деградированном
        # пути (filter_only_search — все score равны, порядок по updated_at)
        # алфавит подменил бы свежесть выдачи
        order = sorted(enumerate(zip(candidates, blended)),
                       key=lambda icb: (-icb[1][1], icb[0]))
        return [replace(c, score=float(b)) for _, (c, b) in order[:n]]

    raws = [_proximity_raw(pq, c) for c in candidates]
    known = [r for r in raws if r is not None]
    if known:
        kmin, kmax = min(known), max(known)
        span = kmax - kmin
        # ближе (меньше минут) → выше; отсутствие данных по оси → худшая близость 0
        prox_norm = [0.0 if r is None else
                     (1.0 if span == 0 else 1.0 - (r - kmin) / span) for r in raws]
    else:
        prox_norm = [0.0] * len(candidates)     # ни по кому нет данных → сигнала нет
    score_norm = _minmax([c.score for c in candidates])

    blended = [w * p + (1.0 - w) * s + o
              for p, s, o in zip(prox_norm, score_norm, orient_bonus)]
    order = sorted(zip(candidates, blended),
                   key=lambda cb: (-cb[1], cb[0].external_id))
    return [replace(c, score=float(b)) for c, b in order[:n]]
