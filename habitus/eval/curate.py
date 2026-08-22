# habitus/eval/curate.py — перекурирование golden-set под текущие данные
"""Пересборка эталона выдачи (`relevant_ids` + `relevance`) в queries.yaml.

Эталон строится ПРАВИЛАМИ по колонкам БД, а не прогоном ранжирующего стека:
иначе eval измерял бы «возвращает ли система то, что она возвращает».
Ни dense, ни sparse, ни реранкер здесь не участвуют.

Релевантность = насколько объект удовлетворяет тому, что просил пользователь:
жёсткие ограничения (комнаты/бюджет/площадь) отсекают, гео и тишина — ранжируют.
"""
import sys
from pathlib import Path

import psycopg
import yaml

from habitus.config import settings
from habitus.online.retrieval import NOISE_ORDER

GOLDEN = Path(__file__).parent / "queries.yaml"
TOP_N = 10

# Радиус, в котором enrich усредняет модельные дБ (habitus/geo/enrich.py).
NOISE_RADIUS_M = 500

# Нижняя граница правдоподобия цены. Медиана по базе — 650 тыс. ₽/м², 5-й
# перцентиль — 317 тыс.; ниже 100 тыс. лежат 11 объектов вроде «154 м² за
# 1.45 млн» (9 тыс. ₽/м²) — доли, торги или ошибка парсинга. В эталон такие
# попадать не должны: человек не назовёт аномалию лучшим ответом.
MIN_PRICE_PER_SQM = 100_000


def _where_and_params(exp: dict, match: dict | None = None) -> tuple[list[str], list]:
    """Клаузы жёстких условий запроса → (список SQL-фрагментов с `%s`, params).

    Вынесено из eligible_rows отдельной чистой функцией — так параметризацию
    (никакой склейки строк с текстом запроса) видно и проверяемо без похода
    в БД: тест собирает where/params и сверяет их напрямую.
    """
    where = ["is_active = TRUE", "city = 'msk'", "geom IS NOT NULL",
             "price IS NOT NULL", "area > 0",
             f"price / area >= {MIN_PRICE_PER_SQM}"]
    params: list = []
    if exp.get("rooms"):
        where.append("rooms = ANY(%s)"); params.append(list(exp["rooms"]))
    if exp.get("price_max") is not None:
        where.append("price <= %s"); params.append(exp["price_max"])
    if exp.get("price_min") is not None:
        where.append("price >= %s"); params.append(exp["price_min"])
    if exp.get("area_min") is not None:
        where.append("area >= %s"); params.append(exp["area_min"])
    if exp.get("area_max") is not None:
        where.append("area <= %s"); params.append(exp["area_max"])
    for g in exp.get("geo") or []:
        col = f"walk_min_{g['kind']}"
        where.append(f"{col} IS NOT NULL AND {col} <= %s")
        params.append(g["walk_minutes"])
    if exp.get("stop_factors") and "bars" in exp["stop_factors"]:
        where.append("bar_density_500m = 0")
    # «тихо» — такое же жёсткое условие, как комнаты и бюджет: объект, который
    # продукт не вправе показать, не может быть эталонным ответом. Границы те же,
    # что в build_where (noise_max — потолок, а не точное значение).
    if exp.get("noise_max") and exp["noise_max"] != "high":
        allowed = NOISE_ORDER[: NOISE_ORDER.index(exp["noise_max"]) + 1]
        where.append("noise_level = ANY(%s)"); params.append(allowed)
    if match:
        # Текстовые оси (адрес/метро по названию), которые build_where не умеет
        # фильтровать вовсе: район/улица в адресе, станция метро. Условие
        # проверяется ПРАВИЛОМ здесь же, в эталоне, а не в retrieval — измеряется
        # именно то, находит ли dense/sparse/реранк такой объект по семантике
        # doc_text, а не подмешивается ли SQL-фильтр, которого в пайплайне нет.
        # Параметризованный ILIKE — никакой склейки строк с паттерном.
        if match.get("address_ilike"):
            where.append("address ILIKE %s"); params.append(match["address_ilike"])
        if match.get("metro_ilike"):
            where.append("metro_station ILIKE %s"); params.append(match["metro_ilike"])
    return where, params


def eligible_rows(conn, exp: dict, match: dict | None = None) -> list[dict]:
    """Объекты, проходящие ЖЁСТКИЕ условия запроса, с полями для ранжирования."""
    where, params = _where_and_params(exp, match)
    sql = f"""
        SELECT external_id, price, rooms, area,
               walk_min_school, walk_min_metro, walk_min_park, bar_density_500m,
               (SELECT avg(e.db) FROM urban_evidence e
                 WHERE e.city = l.city AND e.layer = 'noise'
                   AND ST_DWithin(e.geom::geography, l.geom::geography, {NOISE_RADIUS_M})
               ) AS noise_db
        FROM listings l
        WHERE {' AND '.join(where)}
    """
    with conn.cursor() as cur:
        cur.execute(sql, params)
        cols = [d[0] for d in cur.description]
        return [dict(zip(cols, r)) for r in cur.fetchall()]


def score(row: dict, exp: dict) -> float:
    """Чем ближе к тому, что просили, тем выше. 0..1 по каждой составляющей."""
    parts: list[float] = []
    for g in exp.get("geo") or []:
        limit = float(g["walk_minutes"])
        walk = float(row[f"walk_min_{g['kind']}"])
        parts.append(max(0.0, (limit - walk) / limit))  # ровно на пороге → 0
    if exp.get("noise_max") == "low":
        db = row["noise_db"]
        # 45 дБ и тише — идеально тихо, 75 и громче — совсем шумно
        parts.append(1.0 if db is None else
                     max(0.0, min(1.0, (75.0 - float(db)) / 30.0)))
    if not parts:
        return 0.0
    return sum(parts) / len(parts)


def grade(i: int, n: int) -> int:
    """Позиция в отсортированной выборке → оценка 3/2/1 (верхняя треть, средняя,
    остальные). Доли, а не фиксированные позиции: на коротком пуле иначе все
    получают 3, и NDCG перестаёт различать хороший ответ от приемлемого."""
    if i < max(1, round(n * 0.3)):
        return 3
    if i < max(2, round(n * 0.6)):
        return 2
    return 1


def curate(conn, item: dict) -> tuple[list[str], dict[str, int], int]:
    exp = item.get("expected_parse") or {}
    rows = eligible_rows(conn, exp, item.get("match"))
    # тай-брейк по external_id — прогон обязан быть воспроизводимым
    rows.sort(key=lambda r: (-score(r, exp), r["external_id"]))
    top = rows[:TOP_N]
    ids = [r["external_id"] for r in top]
    grades = {eid: grade(i, len(ids)) for i, eid in enumerate(ids)}
    return ids, grades, len(rows)


def main() -> int:
    with open(GOLDEN, encoding="utf-8") as f:
        golden = yaml.safe_load(f)

    with psycopg.connect(settings.db_dsn) as conn:
        for item in golden:
            # Курируем то, что выражается правилами по колонкам: либо эталон уже
            # есть (перекурирование), либо запрос явно помечен curate: true.
            # Флаг нужен для новых структурных запросов — без него запрос с
            # пустым relevant_ids не мог получить эталон НИКОГДА, и b-серия
            # оставалась мёртвой. Свободная семантика («старый центр», «лофт»)
            # флага не имеет: правилами её не построить, размечает человек.
            if not (item.get("relevant_ids") or item.get("curate")):
                continue
            old = set(item["relevant_ids"])
            ids, grades, pool = curate(conn, item)
            item["relevant_ids"] = ids
            item["relevance"] = grades
            kept = len(old & set(ids))
            print(f"{item['id']}: пул {pool:4d} → эталон {len(ids):2d}, "
                  f"совпало со старым {kept}/{len(old)}")

    with open(GOLDEN, "w", encoding="utf-8") as f:
        yaml.safe_dump(golden, f, allow_unicode=True, sort_keys=False, width=100)
    return 0


if __name__ == "__main__":
    sys.exit(main())
