#!/usr/bin/env python
"""Детерминированная сборка золотого сета `habitus/eval/queries.yaml` из БД.

Метки НЕ проставляются руками — они выводятся из данных по документированному
правилу, поэтому воспроизводимы и аудируемы (как `build_moscow_subset.py`).

Дизайн:
  Tier-A (retrieval-scored) — реалистичный мульти-констрейнт запрос: жёсткие
    фильтры (комнаты/цена/площадь/без-баров/тихо) + ось близости, которую запрос
    ЯВНО просит. relevant_ids = топ-N объектов пула по этой оси (genuine best);
    relevance градуирована по близости (3/2/1) → NDCG осмыслен.
    Фильтры подобраны так, что пул ~15–80 — ранжирование в топ-10 реально решает,
    а метки не «10 произвольных из тысячи».
  Tier-B (parse-only) — чистый фильтр / окна / чистая семантика: relevant_ids=[]
    (в источнике нет прозы/окон — retrieval неразличим), нужны только для
    parse-accuracy.

Запуск:  uv run python scripts/build_golden.py --write
Без --write печатает лишь диагностику пула/сепарации/грейдов.
"""
import argparse
from collections import OrderedDict

import psycopg
import yaml

from habitus.config import settings
from habitus.online.retrieval import build_where
from habitus.online.schema import ParsedQuery

N = 10  # размечаем топ-N по оси

# (id, lang, query, expected_parse, order_sql, grade_expr, t3, t2)
#   grade: <=t3 → 3, <=t2 → 2, иначе 1  (в минутах пешком по оси)
# Пороги калиброваны под циановский срез (премиум-Москва: медиана 2к≈35 млн,
# 3к≈64 млн — старые Kaggle-пороги «до 15–18 млн» давали пул 0). «Без баров»
# убран из Tier-A: у центральных премиум-объектов лишь ~12% без баров в 500 м —
# фильтр схлопывал пул; stop_factors покрыты в Tier-B (parse-only).
TIER_A = [
    ("a01", "ru", "тихая двушка до 40 млн, школа в 10 минутах пешком",
     dict(rooms=[2], price_max=40000000, noise_max="low",
          geo=[{"kind": "school", "walk_minutes": 10}]),
     "walk_min_school", "walk_min_school", 2, 3),
    ("a02", "ru", "двушка до 40 млн, метро в 7 минутах, тихо",
     dict(rooms=[2], price_max=40000000, noise_max="low",
          geo=[{"kind": "metro", "walk_minutes": 7}]),
     "walk_min_metro", "walk_min_metro", 1, 2),
    ("a03", "ru", "однушка до 25 млн, парк в 8 минутах",
     dict(rooms=[1], price_max=25000000,
          geo=[{"kind": "park", "walk_minutes": 8}]),
     "walk_min_park", "walk_min_park", 1, 2),
    ("a04", "en", "two-room flat under 40M, metro within 7 minutes, quiet",
     dict(rooms=[2], price_max=40000000, noise_max="low",
          geo=[{"kind": "metro", "walk_minutes": 7}]),
     "walk_min_metro", "walk_min_metro", 1, 2),
    ("a05", "ru", "трёшка до 70 млн, метро в 8 минутах",
     dict(rooms=[3], price_max=70000000,
          geo=[{"kind": "metro", "walk_minutes": 8}]),
     "walk_min_metro", "walk_min_metro", 2, 3),
    ("a06", "ru", "однушка или двушка до 35 млн, школа и метро рядом",
     dict(rooms=[1, 2], price_max=35000000,
          geo=[{"kind": "school", "walk_minutes": 15},
               {"kind": "metro", "walk_minutes": 15}]),
     "(walk_min_school+walk_min_metro)", "(walk_min_school+walk_min_metro)", 6, 10),
    ("a07", "ru", "тихая трёшка до 75 млн, школа в 10 минутах",
     dict(rooms=[3], price_max=75000000, noise_max="low",
          geo=[{"kind": "school", "walk_minutes": 10}]),
     "walk_min_school", "walk_min_school", 3, 5),
    ("a08", "en", "one or two room flat up to 30M, school within 9 minutes",
     dict(rooms=[1, 2], price_max=30000000,
          geo=[{"kind": "school", "walk_minutes": 9}]),
     "walk_min_school", "walk_min_school", 2, 3),
    ("a09", "ru", "трёшка или четвёрка до 90 млн, парк и метро в 10 минутах",
     dict(rooms=[3, 4], price_max=90000000,
          geo=[{"kind": "park", "walk_minutes": 10},
               {"kind": "metro", "walk_minutes": 10}]),
     "(walk_min_park+walk_min_metro)", "(walk_min_park+walk_min_metro)", 3, 5),
    ("a10", "ru", "двушка до 50 млн, метро в 10 минутах",
     dict(rooms=[2], price_max=50000000,
          geo=[{"kind": "metro", "walk_minutes": 10}]),
     "walk_min_metro", "walk_min_metro", 1, 2),
]

# Tier-B parse-only — разнообразие для parse-accuracy (retrieval-метки пусты)
TIER_B = [
    ("b01", "ru", "двушка, 45-60 метров, бюджет до 12 млн",
     dict(rooms=[2], area_min=45.0, area_max=60.0, price_max=12000000)),
    ("b02", "en", "one-room apartment, budget up to 8 million rubles",
     dict(rooms=[1], price_max=8000000, lang="en")),
    ("b03", "ru", "трёшка от 60 метров, окна на юго-запад, метро в 7 минутах",
     dict(rooms=[3], area_min=60.0, window_orientation=["SW"],
          geo=[{"kind": "metro", "walk_minutes": 7}])),
    ("b04", "ru", "двор-колодец, атмосфера старого центра, брусчатка во дворе",
     dict()),
    ("b05", "en", "loft with high ceilings and exposed brick", dict(lang="en")),
    ("b06", "ru", "не хочу коммуналок и шумных дворов",
     dict(noise_max="low", stop_factors=["communal_flats"])),
]


def build(conn, verbose=True):
    docs = []
    if verbose:
        print(f"{'id':4} {'pool':>5} {'topN_max':>8} {'pool_med':>8}  grades")
    for qid, lang, query, ep, order, gexpr, t3, t2 in TIER_A:
        pq = ParsedQuery.model_validate({**ep, "semantic_text": query})
        where, params = build_where(pq)
        with conn.cursor() as cur:
            cur.execute(f"SELECT count(*) FROM listings WHERE {where}", params)
            pool = cur.fetchone()[0]
            cur.execute(f"SELECT external_id, {gexpr} v FROM listings WHERE {where} "
                        f"ORDER BY {order} ASC LIMIT {N}", params)
            rows = cur.fetchall()
            cur.execute(f"SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY {gexpr}) "
                        f"FROM listings WHERE {where}", params)
            med = cur.fetchone()[0]
        rel = OrderedDict((eid, 3 if v <= t3 else (2 if v <= t2 else 1))
                          for eid, v in rows)
        if verbose:
            gdist = {g: list(rel.values()).count(g) for g in (3, 2, 1)}
            print(f"{qid:4} {pool:>5} {rows[-1][1]:>8.2f} {med:>8.2f}  {gdist}")
        docs.append(dict(id=qid, lang=lang, query=query, expected_parse=ep,
                         relevant_ids=list(rel.keys()),
                         relevance={k: int(v) for k, v in rel.items()}))
    for qid, lang, query, ep in TIER_B:
        docs.append(dict(id=qid, lang=lang, query=query, expected_parse=ep,
                         relevant_ids=[]))
    return docs


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--write", action="store_true",
                    help="перезаписать habitus/eval/queries.yaml")
    args = ap.parse_args()
    with psycopg.connect(settings.db_dsn) as conn:
        docs = build(conn)
    if args.write:
        from pathlib import Path
        out = Path(__file__).resolve().parents[1] / "habitus/eval/queries.yaml"
        with open(out, "w", encoding="utf-8") as f:
            yaml.safe_dump(docs, f, allow_unicode=True, sort_keys=False, width=200)
        n_lab = sum(1 for d in docs if d["relevant_ids"])
        print(f"WROTE {out} ({len(docs)} запросов, {n_lab} размечено)")
