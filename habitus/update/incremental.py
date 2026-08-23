import logging
from datetime import datetime, timedelta

import psycopg
from habitus.geo.osm_extract import upsert_poi
from habitus.geo.enrich import enrich_around

log = logging.getLogger("habitus.update.incremental")

# Какую долю активной базы должен покрыть снимок, чтобы по нему вообще можно
# было гасить. 0.5 — не подобранное число: щадящий обход берёт 400 объявлений
# при базе в тысячи (доля ~0.1), полный — почти всю базу (доля ~1.0), и
# половина лежит в широком зазоре между этими режимами.
SNAPSHOT_COVERAGE_MIN = 0.5

# Порог, отделяющий один обход источника от другого. Обход 1500 объявлений
# укладывается в пять минут, а прогоны идут раз в 6 часов — между этими
# величинами два порядка, так что час здесь не «подобранная» константа, а
# любое значение из широкой безопасной середины.
CRAWL_GAP = timedelta(hours=1)

# Сколько последних обходов образуют «свежий снимок». Один обход ненадёжен:
# источник обрывает пагинацию капчей, и усечённый обход выглядит как массовое
# снятие с продажи. Два обхода — компромисс между ложным гашением и задержкой.
CRAWLS_KEPT = 2


def _parsed_at(row: dict) -> datetime | None:
    raw = str(row.get("collected_at") or "").strip()
    if not raw:
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        return None


def latest_snapshot_ids(rows: list[dict], gap: timedelta = CRAWL_GAP,
                        crawls: int = CRAWLS_KEPT) -> set[str]:
    """external_id последних `crawls` обходов, а не всего накопленного файла.

    Сборщик пишет CSV через latest-wins merge по внешнему id: строка, однажды
    попавшая в файл, остаётся там навсегда. Поэтому «чего нет в файле» никогда
    не выполняется, и гашение, построенное на всём файле, молча не работает.

    Окно шире одного обхода намеренно. Обход обрывается: источник отдаёт капчу
    посреди пагинации, и живое объявление, не попавшее в усечённый обход,
    неотличимо от снятого с продажи. Двух обходов достаточно, чтобы один
    сорванный не гасил чужое, и мало, чтобы проданное задерживалось надолго.

    Строки без разбираемого времени сбора попадают в снимок ВСЕГДА: не зная
    даты, мы не вправе утверждать, что объявление снято с продажи. Из-за этого
    выгрузка без времени (Kaggle) сохраняет прежнее поведение.
    """
    dated = [(t, r) for r in rows if (t := _parsed_at(r)) is not None]
    undated = {r["external_id"] for r in rows if _parsed_at(r) is None}
    if not dated:
        return undated

    # Метки времени внутри обхода идут вплотную (обход 2366 объявлений уложился
    # в 9 минут), между обходами — часы. Кластеризуем по разрыву, а не по
    # календарным суткам: обход может начаться до полуночи и кончиться после.
    times = sorted({t for t, _ in dated}, reverse=True)
    cutoff, seen, prev = times[-1], 0, times[0]
    for t in times[1:]:
        if prev - t > gap:          # разрыв — начался предыдущий обход
            seen += 1
            if seen >= crawls:      # набрали нужное число, prev — их нижняя граница
                cutoff = prev
                break
        prev = t
    return undated | {r["external_id"] for t, r in dated if t >= cutoff}


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

    Пустой снимок не гасит ничего. Сорванный обход (капча, сеть, пустой файл)
    неотличим по данным от «всё снято с продажи», и цена ошибки несимметрична:
    обнулить базу дорого, пропустить один цикл гашения — нет.

    По той же причине не гасим по ЧАСТИЧНОМУ снимку. Щадящий режим сбора
    (HABITUS_MAX_OFFERS=400, см. scripts/refresh.sh и
    docs/notes/cian-blocked-2026-08-17.md) снимает первые страницы выдачи, а не
    всю базу: объявление, которого нет в таком снимке, скорее «не долистали»,
    чем «снято с продажи». Без этого порога один щадящий цикл гасит всё, что
    собрали предыдущие, — база схлопывается до размера одного снимка.
    Настоящее гашение по частичным снимкам требует отметки «когда видели
    последний раз», это отдельная задача.
    """
    if not active_ids:
        return 0
    # owner_managed исключается всегда: объявление продавца не приходит ни в
    # одном снимке источника, поэтому «его нет в снимке» ничего не означает.
    where = "is_active = true AND NOT owner_managed" + (" AND source = %s" if source else "")
    params = (source,) if source else ()
    with conn.cursor() as cur:
        cur.execute(f"SELECT external_id FROM listings WHERE {where};", params)
        current = {r[0] for r in cur.fetchall()}
        if len(active_ids) < len(current) * SNAPSHOT_COVERAGE_MIN:
            log.warning(
                "гашение пропущено: снимок %d объявлений против %d активных "
                "(меньше %.0f%%) — похоже на частичный обход, а не на снятие "
                "с продажи", len(active_ids), len(current),
                SNAPSHOT_COVERAGE_MIN * 100)
            return 0
        missing = current - active_ids
        if missing:
            cur.execute("UPDATE listings SET is_active=false, updated_at=now() "
                        "WHERE external_id = ANY(%s);", (list(missing),))
    conn.commit()
    return len(missing)
