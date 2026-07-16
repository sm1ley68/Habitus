import argparse
from pathlib import Path
import psycopg
from psycopg.rows import dict_row
from habitus.config import settings
from habitus.db.init_db import init_db
from habitus.db.connection import get_conn
from habitus.ingest.kaggle_loader import parse_csv as parse_kaggle_csv, load_to_raw
from habitus.ingest.cian_loader import parse_csv as parse_cian_csv
from habitus.clean.normalize import promote_to_listings
from habitus.clean.geocode import backfill_missing_coords
from habitus.geo.osm_extract import fetch_kind, upsert_poi, OVERPASS_QUERIES
from habitus.geo.enrich import enrich_all
from habitus.embed.document import build_doc_text
from habitus.embed.encode import embed_pending


def _refresh_doc_text(conn) -> int:
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("SELECT * FROM listings;")
        rows = cur.fetchall()
    with conn.cursor() as cur:
        for r in rows:
            cur.execute("UPDATE listings SET doc_text=%s WHERE external_id=%s;",
                        (build_doc_text(r), r["external_id"]))
    conn.commit()
    return len(rows)


_PARSERS = {"kaggle": parse_kaggle_csv, "cian": parse_cian_csv}


def run_offline(csv_path: Path, conn, model=None, fetch_osm=True, geocoder=None,
                source="kaggle") -> dict:
    init_db(conn)
    stats = {}
    stats["raw"] = load_to_raw(_PARSERS[source](csv_path), conn)
    stats["listings"] = promote_to_listings(conn)
    geo_kwargs = {} if geocoder is None else {"geocoder": geocoder}
    stats["geocoded"] = backfill_missing_coords(conn, **geo_kwargs)
    if fetch_osm:
        for kind in OVERPASS_QUERIES:
            upsert_poi(fetch_kind(kind), conn)
    stats["enriched"] = enrich_all(conn)
    stats["doc_text"] = _refresh_doc_text(conn)
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
    evidence = sub.add_parser("import-evidence")
    evidence.add_argument("--geojson", type=Path, required=True)
    sub.add_parser("import-osm-features")
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
            from habitus.eval.runner import (DEFAULT_GOLDEN, format_report,
                                             load_golden, run_eval)
            from habitus.online.llm import OpenRouterLLM
            llm = OpenRouterLLM() if settings.openrouter_api_key else None
            golden = load_golden(args.golden or DEFAULT_GOLDEN)
            print(format_report(run_eval(conn, llm, golden)))
        elif args.cmd == "import-evidence":
            from habitus.geo.evidence import import_geojson_file
            init_db(conn)
            print({"imported": import_geojson_file(args.geojson, conn)})
        elif args.cmd == "import-osm-features":
            from habitus.geo.osm_extract import (fetch_urban_features,
                                                  upsert_urban_features)
            init_db(conn)
            print({"imported": upsert_urban_features(fetch_urban_features(), conn)})


if __name__ == "__main__":
    main()
