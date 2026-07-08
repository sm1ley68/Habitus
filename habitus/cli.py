import argparse
from pathlib import Path
import psycopg
from psycopg.rows import dict_row
from habitus.db.init_db import init_db
from habitus.db.connection import get_conn
from habitus.ingest.kaggle_loader import parse_csv, load_to_raw
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


def run_offline(csv_path: Path, conn, model=None, fetch_osm=True) -> dict:
    init_db(conn)
    stats = {}
    stats["raw"] = load_to_raw(parse_csv(csv_path), conn)
    stats["listings"] = promote_to_listings(conn)
    stats["geocoded"] = backfill_missing_coords(conn)
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
    off.add_argument("--no-osm", action="store_true")
    sub.add_parser("update")
    args = ap.parse_args()
    with get_conn() as conn:
        if args.cmd == "offline":
            print(run_offline(args.csv, conn, fetch_osm=not args.no_osm))
        elif args.cmd == "update":
            print("update: запускать по cron (инкрементал)")


if __name__ == "__main__":
    main()
