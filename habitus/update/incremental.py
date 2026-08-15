import psycopg
from habitus.geo.osm_extract import upsert_poi
from habitus.geo.enrich import enrich_around


def apply_new_poi(rows: list[dict], conn: psycopg.Connection) -> int:
    upsert_poi(rows, conn)
    affected = 0
    for r in rows:
        wkt = f"POINT({r['lon']} {r['lat']})"
        affected += enrich_around(conn, wkt)
    return affected


def deactivate_missing(active_ids: set[str], conn: psycopg.Connection,
                       source: str | None = None) -> int:
    """Гасит объявления источника, которых больше нет в его свежем снимке.

    `source` обязателен на практике: external_id разных источников не
    пересекаются, поэтому снимок Циана без скоупа погасил бы вообще всё чужое.
    Обратное включение делает promote_to_listings — там is_active=true в
    ON CONFLICT, так что вернувшееся в продажу объявление оживает само.
    """
    where = "is_active = true" + (" AND source = %s" if source else "")
    params = (source,) if source else ()
    with conn.cursor() as cur:
        cur.execute(f"SELECT external_id FROM listings WHERE {where};", params)
        current = {r[0] for r in cur.fetchall()}
        missing = current - active_ids
        if missing:
            cur.execute("UPDATE listings SET is_active=false, updated_at=now() "
                        "WHERE external_id = ANY(%s);", (list(missing),))
    conn.commit()
    return len(missing)
