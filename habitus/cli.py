import argparse
import sys
from pathlib import Path
import psycopg
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.db.connection import get_conn
from habitus.ingest.kaggle_loader import parse_csv as parse_kaggle_csv, load_to_raw
from habitus.ingest.cian_loader import parse_csv as parse_cian_csv
from habitus.clean.normalize import promote_to_listings
from habitus.update.incremental import deactivate_missing, latest_snapshot_ids
from habitus.clean.geocode import backfill_missing_coords
from habitus.geo.osm_extract import fetch_kind, upsert_poi, OVERPASS_QUERIES
from habitus.geo.enrich import enrich_all
from habitus.embed.document import refresh_doc_text
from habitus.embed.encode import embed_pending


_PARSERS = {"kaggle": parse_kaggle_csv, "cian": parse_cian_csv}

# Пороги гейта `eval --check`: измеренные precision@10/NDCG@10 варианта
# rrf+rerank+prox (полный golden-set, a+b+c серии) минус 0.05 — запас под шум
# одного прогона, а не под ожидаемое улучшение. Precision, а не recall: у
# запросов без ранжирующего сигнала релевантен весь пул, и recall@10 у них
# зависит от размера базы, а не от качества поиска. Источник измерения:
# docs/notes/eval-baseline-2026-08-18.md; менять оба значения только вместе
# с новым прогоном и новой записью в заметке.
_DEFAULT_MIN_PRECISION = 0.40
_DEFAULT_MIN_NDCG = 0.41


def run_offline(csv_path: Path, conn, model=None, fetch_osm=True, geocoder=None,
                source="kaggle") -> dict:
    init_db(conn)
    stats = {}
    rows = _PARSERS[source](csv_path)
    stats["raw"] = load_to_raw(rows, conn)
    stats["listings"] = promote_to_listings(conn)
    # Снятое с продажи должно уходить из выдачи: всё, чего нет в свежем снимке
    # ЭТОГО источника, гасим. Вернувшееся объявление оживит promote_to_listings
    # (is_active=true в ON CONFLICT). Скоуп по источнику обязателен — иначе
    # прогон Циана погасил бы объявления Kaggle.
    #
    # Снимок — последний обход, а НЕ весь файл: CSV сборщика накапливается и
    # никогда не уменьшается, поэтому сравнение с ним давало deactivated=0
    # в каждом цикле при полностью активной базе.
    stats["deactivated"] = deactivate_missing(
        latest_snapshot_ids(rows), conn, source=source)
    geo_kwargs = {} if geocoder is None else {"geocoder": geocoder}
    stats["geocoded"] = backfill_missing_coords(conn, **geo_kwargs)
    # Overpass — чужой публичный API, он регулярно отдаёт 504 и рвёт соединение
    # (четыре ночных цикла подряд упали именно на нём). Точки города меняются
    # раз в месяцы, объявления — каждый час, поэтому провал загрузки POI не
    # должен уносить с собой заливку, обогащение и эмбеддинги. Сбой не глотаем
    # молча: список провалившихся слоёв уезжает в статистику цикла.
    stats["osm_failed"] = []
    if fetch_osm:
        for kind in OVERPASS_QUERIES:
            try:
                upsert_poi(fetch_kind(kind), conn)
            except Exception as e:  # noqa: BLE001 — внешний API, причин отказа много
                conn.rollback()
                stats["osm_failed"].append(f"{kind}: {e}")
    stats["enriched"] = enrich_all(conn)
    stats["doc_text"] = refresh_doc_text(conn)
    stats["embedded"] = embed_pending(conn, model=model)
    return stats


def main():
    ap = argparse.ArgumentParser(prog="habitus")
    sub = ap.add_subparsers(dest="cmd", required=True)
    off = sub.add_parser("offline")
    off.add_argument("--csv", type=Path, required=True)
    off.add_argument("--source", choices=["kaggle", "cian"], default="kaggle")
    off.add_argument("--no-osm", action="store_true")
    sub.add_parser("update")
    s = sub.add_parser("search")
    s.add_argument("query")
    ev = sub.add_parser("eval")
    ev.add_argument("--golden", type=Path, default=None)
    ev.add_argument("--check", action="store_true",
                    help="ненулевой код возврата при просадке ниже порогов")
    # Дефолты — измеренное значение rrf+rerank+prox минус 0.05 (запас под шум
    # прогона), зафиксировано после расширения golden-set c-серией:
    # docs/notes/eval-baseline-2026-08-18.md. Обновлять вместе с этой заметкой.
    ev.add_argument("--min-precision", type=float, default=_DEFAULT_MIN_PRECISION)
    ev.add_argument("--min-ndcg", type=float, default=_DEFAULT_MIN_NDCG)
    evidence = sub.add_parser("import-evidence")
    evidence.add_argument("--geojson", type=Path, required=True)
    zones = sub.add_parser("import-zones")
    zones.add_argument("--geojson", type=Path, required=True)
    zones.add_argument("--named", type=Path,
                       default=Path("data/named_zones.seed.json"))
    sub.add_parser("import-osm-features")
    windows = sub.add_parser("extract-windows")
    windows.add_argument("--limit", type=int, default=None)
    args = ap.parse_args()
    with get_conn() as conn:
        if args.cmd == "offline":
            print(run_offline(args.csv, conn, fetch_osm=not args.no_osm,
                              source=args.source))
        elif args.cmd == "update":
            print("update: запускать по cron (инкрементал)")
        elif args.cmd == "search":
            from habitus.online.llm import OpenRouterLLM
            from habitus.online.pipeline import run_search
            llm = OpenRouterLLM() if settings.openrouter_api_key else None
            resp = run_search(args.query, conn, llm=llm)
            for i, r in enumerate(resp.results, 1):
                print(f"{i}. {r.external_id} | {r.price} ₽ | {r.rooms}-комн | "
                      f"{r.area} м² | score={r.score:.3f}")
            print("\n" + resp.explanation)
            if resp.relaxed:
                print("Ослаблено: " + "; ".join(resp.relaxed))
            if resp.degraded:
                print("Деградация: " + ", ".join(resp.degraded))
            print(resp.data_freshness)
        elif args.cmd == "eval":
            from habitus.eval.runner import (DEFAULT_GOLDEN, check_thresholds,
                                             format_report, load_golden, run_eval)
            from habitus.online.llm import OpenRouterLLM
            llm = OpenRouterLLM() if settings.openrouter_api_key else None
            golden = load_golden(args.golden or DEFAULT_GOLDEN)
            res = run_eval(conn, llm, golden)
            print(format_report(res))
            if args.check:
                failures = check_thresholds(res, args.min_precision, args.min_ndcg)
                if failures:
                    print("\nГЕЙТ НЕ ПРОЙДЕН:")
                    for f in failures:
                        print(f"  - {f}")
                    sys.exit(1)
                print("\nгейт пройден")
        elif args.cmd == "import-evidence":
            from habitus.geo.evidence import import_geojson_file
            init_db(conn)
            print({"imported": import_geojson_file(args.geojson, conn)})
        elif args.cmd == "import-zones":
            from habitus.geo.zones import (backfill_listing_zones,
                                           import_admin_geojson,
                                           import_named_seed)
            init_db(conn)
            a = import_admin_geojson(args.geojson, conn)
            n = import_named_seed(args.named, conn) if args.named.exists() else 0
            b = backfill_listing_zones(conn)
            print({"admin_zones": a, "named_zones": n, "listings_backfilled": b})
        elif args.cmd == "import-osm-features":
            from habitus.geo.osm_extract import (fetch_urban_features,
                                                  upsert_urban_features)
            init_db(conn)
            print({"imported": upsert_urban_features(fetch_urban_features(), conn)})
        elif args.cmd == "extract-windows":
            from habitus.clean.windows import extract_windows
            from habitus.online.llm import OpenRouterLLM
            print(extract_windows(conn, OpenRouterLLM(), limit=args.limit))


if __name__ == "__main__":
    main()
