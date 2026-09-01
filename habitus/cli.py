import argparse
import logging
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
from habitus.geo.osm_extract import fetch_kind, upsert_poi, POI_KINDS
from habitus.geo.enrich import enrich_all
from habitus.geo.metro import SYSTEMS, fetch_system, upsert_transit
from habitus.geo.metro_access import (ORSWalker, refresh_listing_metro_access,
                                      refresh_walk_min_metro)
from habitus.embed.document import refresh_doc_text
from habitus.embed.encode import embed_pending

log = logging.getLogger(__name__)

_PARSERS = {"kaggle": parse_kaggle_csv, "cian": parse_cian_csv}

# Пороги гейта `eval --check`: измеренные precision@10/NDCG@10 варианта
# rrf+rerank+prox минус 0.05 — запас под шум одного прогона, а не под
# ожидаемое улучшение. Precision, а не recall: у запросов без ранжирующего
# сигнала релевантен весь пул, и recall@10 у них зависит от размера базы, а
# не от качества поиска. Источник измерения:
# docs/notes/eval-baseline-2026-08-18.md; менять оба значения только вместе
# с новым прогоном и новой записью в заметке.
#
# R94 (сквозное ревью ветки): числа выведены по ПОЛНОМУ набору a+b+c
# (20 запросов, десятизапросная a-серия), но сегодня гейт стоит не на нём.
# Таблица `poi` пуста, walk_min_school/walk_min_park — NULL у всех
# объявлений, поэтому шесть a-запросов про школу и парк остались без
# эталона, и `eval` считает метрики на 14 запросах из 22 — a-серия усохла
# до четырёх запросов, ВСЕ по метро (замер и разбор:
# docs/notes/metro-rollout-2026-08-29.md). Гейт сейчас чувствителен к b+c и
# к урезанной a; пороги не менялись и в этом состоянии держатся (0.58/0.59),
# но baseline обязан быть ПЕРЕИЗМЕРЕН после наполнения `poi` — до тех пор
# сравнивать нынешние числа с прогонами на полном наборе нельзя.
_DEFAULT_MIN_PRECISION = 0.40
_DEFAULT_MIN_NDCG = 0.41


def run_offline(csv_path: Path, conn, model=None, fetch_osm=True, geocoder=None,
                source="kaggle", city: str = "msk", no_ors: bool = False) -> dict:
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
        for kind in POI_KINDS:
            try:
                upsert_poi(fetch_kind(kind, city), conn, city=city)
            except Exception as e:  # noqa: BLE001 — внешний API, причин отказа много
                conn.rollback()
                stats["osm_failed"].append(f"{kind}/{city}: {e}")
    stats["enriched"] = enrich_all(conn)
    if fetch_osm:
        # fetch=fetch_system передан явно (а не оставлен на дефолт параметра
        # build_metro): это делает имя живым lookup'ом в глобалах модуля на
        # момент вызова — тем же приёмом, что fetch_kind чуть выше — и
        # monkeypatch.setattr(cli, "fetch_system", ...) в тестах реально
        # подменяет то, что уйдёт в build_metro, а не бьётся о дефолт,
        # захваченный один раз при определении функции.
        # walker: тот же опт-ин по ORS_API_KEY, что и в подкоманде `metro`
        # (R49) — --no-osm выключает и POI, и метро разом, поэтому нужен
        # отдельный --no-ors, который выключает только внешний пеший роутер,
        # не трогая ни POI, ни сбор графа метро/МЦК/МЦД из OSM.
        stats["metro"] = build_metro(
            conn, city, fetch=fetch_system,
            walker=None if no_ors or not settings.ors_api_key else ORSWalker())
    stats["doc_text"] = refresh_doc_text(conn)
    stats["embedded"] = embed_pending(conn, model=model)
    return stats


def build_metro(conn, city: str, fetch=fetch_system, walker=None) -> dict:
    """Граф рельсового транспорта города: OSM → БД → пешие плечи объектов.

    Отказ одной системы не уносит остальные и не глотается молча — тем же
    принципом, которым защищён сбор POI: Overpass регулярно отдаёт 504.

    R36 (ruling): upsert_transit САМ по себе только апсертит — линия,
    пропавшая из OSM (закрыли участок, переразметили релейшен под другим
    ref), иначе жила бы в графе вечно и маршрутизировалась бы как настоящая.
    Решение — delete-missing ПО СИСТЕМЕ после каждого успешного fetch, а не
    общий TRUNCATE metro_line CASCADE в начале функции: TRUNCATE стирает ВСЕ
    города и ВСЕ системы разом, а провал fetch одной системы (Overpass 504)
    тогда навсегда обнулял бы её прежние, ещё валидные данные вместо того,
    чтобы их сохранить до следующего успешного прогона — ровно тот компромисс,
    от которого защищает try/except ниже. Delete-missing запускается только
    для систем, fetch которых УДАЛСЯ: ref, которых нет в свежем списке линий
    этой системы для этого города, удаляются; каскад с metro_line дотягивается
    до metro_station → metro_edge/metro_transfer/metro_line_geom и
    listing_metro_access (те же ON DELETE CASCADE, что и в тестовой фикстуре
    TRUNCATE metro_line CASCADE).

    walker=None — оценка по прямой (никаких сетевых вызовов); walker=ORSWalker()
    — реальные пешие маршруты через внешний ORS. На 66k объявлений и k=3 это
    ~200k вызовов ORS за один прогон — далеко за пределами публичной квоты,
    поэтому CLI НЕ включает ORS сам по себе: он используется только если
    оператор явно настроил ORS_API_KEY (settings.ors_api_key) и не передал
    --no-ors. Без ключа поведение как раньше — оценка по прямой.
    """
    stats: dict = {"failed": []}
    for system in SYSTEMS:
        try:
            lines = fetch(system, city)
            if not lines:
                # Пустой, но УСПЕШНЫЙ (без исключения) ответ Overpass — не
                # апсертим и не удаляем ничего для этой системы. «Было N
                # линий → стало 0» неотличимо от сбоя формата ответа
                # (потерялись elements где-то в середине рекурсивного `>;`
                # при таймауте) — тот же класс проблем, из-за которого
                # запрос вообще сделан рекурсивным (см. docstring
                # fetch_system). Трактовать пустой список как «система
                # реально опустела» и стирать её прежние данные — риск
                # потерять весь граф молча от одного плохого ответа;
                # трактовать как «ничего не изменилось в этом цикле» — риск
                # на один цикл не заметить настоящее исчезновение системы
                # целиком (для msk/spb это практически не происходит).
                # Второй риск дешевле первого — синтетический ноль в графе
                # запрещён тем же принципом, что и синтетический ноль в
                # досье объекта (CLAUDE.md).
                # "skipped" — явный маркер: ключ отличает «ничего не
                # трогали, потому что fetch вернул 0» от настоящего нуля
                # линий, который дал бы upsert_transit на пустой,
                # неотличимый по форме stats-словарь без этого поля.
                stats[system] = {"lines": 0, "stations": 0, "edges": 0,
                                 "transfers": 0,
                                 "skipped": "пустой успешный fetch — "
                                            "данные системы не тронуты"}
                continue
            stats[system] = upsert_transit(lines, conn, city)
            # R36 (ruling): upsert_transit сам по себе только апсертит —
            # линию, пропавшую из OSM у системы, которая по-прежнему
            # отвечает (закрыли участок, переразметили релейшен под другим
            # ref), никто не удаляет, и она жила бы в графе вечно,
            # маршрутизируясь как настоящая. Delete-missing — ПО СИСТЕМЕ
            # (city, system), а не общий TRUNCATE metro_line CASCADE в
            # начале функции: TRUNCATE стирает разом все города и все
            # системы, и тогда провал fetch другой системы в этом же
            # прогоне (см. except ниже) навсегда обнулял бы её прежние,
            # ещё валидные данные вместо того, чтобы сохранить их до
            # следующего успешного прогона. Каскад с metro_line дотягивается
            # до metro_station → metro_edge/metro_transfer/metro_line_geom
            # и listing_metro_access (те же ON DELETE CASCADE, что и в
            # тестовой фикстуре `TRUNCATE metro_line CASCADE`).
            refs = [line.ref for line in lines]
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT count(*) FROM metro_line "
                    "WHERE city = %s AND system = %s;", (city, system))
                stored = cur.fetchone()[0]
            # R47 (ruling): пустой fetch выше защищён отдельно, но типичный
            # сбой Overpass — "потерялись elements где-то в середине
            # рекурсивного `>;`" (см. докстринг fetch_system) — обычно даёт
            # НЕ пустой, а ЧАСТИЧНЫЙ список: старую защиту это не ловит.
            # Если свежий список меньше половины того, что лежит в БД для
            # этой (city, system) ПОСЛЕ апсерта строкой выше — delete-missing
            # отменяется. `stored` считается уже ПОСЛЕ upsert_transit(lines,
            # ...), а не до: это не «что было в БД до этого прогона», а
            # объединение прежних строк и только что зафетченных (upsert
            # только добавляет/обновляет, ничего не убирает). Из этого же
            # факта следует, что самый первый прогон по городу/системе
            # (stored был 0 до апсерта) никогда не блокируется: `stored`
            # после апсерта равен len(refs), и len(refs) < len(refs) * 0.5
            # ложно при len(refs) > 0 — проверка `stored > 0` ниже здесь для
            # ясности чтения, а не как отдельная защита от этого случая.
            #
            # Порог 0.5, а не более жёсткий: подземка крупного города не
            # теряет между двумя соседними ночными прогонами больше половины
            # линий ни при каком реальном событии — настоящее массовое
            # закрытие такого масштаба само по себе редкое, заметное и
            # переживёт один пропущенный автоматический цикл до ручной
            # проверки; тихая потеря графа от одного битого ответа Overpass
            # — не переживёт. Сравнение строго `<`, а не `<=`: правило
            # сформулировано как «блокируем, когда сохранилось МЕНЬШЕ
            # половины», значит ровно половина — ещё минимально приемлемая
            # доля, а не повод отказывать; выбор границы, а не подгонка под
            # конкретный тест.
            if stored > 0 and len(refs) < stored * 0.5:
                stats["failed"].append(
                    f"{system}/{city}: подозрительно короткий ответ Overpass "
                    f"({len(refs)} линий против {stored} строк metro_line "
                    "этой системы в БД после апсерта — включает и прежние, "
                    "и только что зафетченные ref'ы) — delete-missing "
                    "пропущен, нужна ручная проверка")
                conn.commit()
                continue
            with conn.cursor() as cur:
                cur.execute(
                    "DELETE FROM metro_line WHERE city = %s AND system = %s "
                    "AND NOT (ref = ANY(%s::text[]));",
                    (city, system, refs))
            conn.commit()
        except Exception as e:  # noqa: BLE001 — внешний API, причин отказа много
            conn.rollback()
            stats["failed"].append(f"{system}/{city}: {e}")
    # Пешие плечи — ПОСЛЕ пересборки графа выше, не до: delete-missing мог
    # снести станции (а с ними каскадом и listing_metro_access) тех линий,
    # что пропали из OSM. Посчитай плечи раньше — walk_min_metro у таких
    # объектов молча остался бы на устаревшем значении вместо пересчёта.
    #
    # Обёрнуто в try/except тем же принципом: refresh_listing_metro_access
    # при walker=ORSWalker() уходит во внешний ORS на каждую станцию
    # кандидата (внутренние отказы там уже деградируют ДО оценки по прямой,
    # см. metro_access.py), но городской SELECT/DELETE/INSERT по всем 66k
    # объявлений — это тоже нетривиальный объём работы поверх ЖИВОЙ БД;
    # его отказ не должен превращать уже успешно пересобранный граф линий
    # в незафиксированный результат команды.
    try:
        stats["access"] = refresh_listing_metro_access(conn, city, walker=walker)
        stats["walk_min_metro"] = refresh_walk_min_metro(conn, city)
    except Exception as e:  # noqa: BLE001
        conn.rollback()
        log.error("пересчёт пеших плеч метро провалился (%s): %s", city, e)
        stats["failed"].append(f"access/{city}: {e}")
        # Ключи остаются в словаре с явным сентинелом None (успешный прогон
        # всегда пишет сюда int — количество строк), а не просто исчезают:
        # скриптовый/cron-запуск, который читает stats["access"] напрямую,
        # без осмотра stats["failed"], иначе не отличил бы отказ от того,
        # что этот шаг вовсе не выполнялся.
        stats.setdefault("access", None)
        stats.setdefault("walk_min_metro", None)
    return stats


def main():
    ap = argparse.ArgumentParser(prog="habitus")
    sub = ap.add_subparsers(dest="cmd", required=True)
    off = sub.add_parser("offline")
    off.add_argument("--csv", type=Path, required=True)
    off.add_argument("--source", choices=["kaggle", "cian"], default="kaggle")
    off.add_argument("--no-osm", action="store_true")
    off.add_argument("--no-ors", action="store_true",
                     help="не ходить в ORS: пешие плечи метро оценкой по прямой")
    off.add_argument("--city", choices=["msk", "spb"], default="msk")
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
    metro = sub.add_parser("metro")
    metro.add_argument("--city", choices=["msk", "spb"], default="msk")
    metro.add_argument("--no-ors", action="store_true",
                       help="не ходить в ORS: пешие плечи оценкой по прямой")
    args = ap.parse_args()
    with get_conn() as conn:
        if args.cmd == "offline":
            print(run_offline(args.csv, conn, fetch_osm=not args.no_osm,
                              source=args.source, city=args.city,
                              no_ors=args.no_ors))
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
        elif args.cmd == "metro":
            # init_db — как и во всех остальных пишущих подкомандах
            # (import-evidence, import-zones, import-osm-features, offline):
            # без него на базе, где schema.sql не переигран после Задачи 5,
            # все три системы и пересчёт плеч упадут на "relation metro_line
            # does not exist", а команда всё равно напечатает stats и
            # выйдет с кодом 0 (R48 ruling).
            init_db(conn)
            # walker=None по умолчанию, если ключ ORS не настроен — как и в
            # run_offline: ORS включается ТОЛЬКО опт-ином через
            # ORS_API_KEY, а не молча по факту наличия команды, потому что
            # на 66k объявлений и k=3 это ~200k вызовов внешнего API за один
            # прогон (см. docstring build_metro). --no-ors — явный оверрайд
            # в обратную сторону: оценка по прямой, даже если ключ настроен.
            walker = None if args.no_ors or not settings.ors_api_key else ORSWalker()
            # fetch=fetch_system передан явно — тем же приёмом, что и вызов
            # build_metro из run_offline чуть выше (живой lookup имени в
            # глобалах модуля на момент вызова, а не захваченный один раз
            # дефолт параметра build_metro): без этого monkeypatch.setattr(
            # cli, "fetch_system", ...) в тестах не подменяет то, что реально
            # уйдёт в build_metro из ЭТОЙ ветки, и тест либо не может
            # застабить её вовсе, либо реально стучится в Overpass (R52).
            stats = build_metro(conn, args.city, fetch=fetch_system, walker=walker)
            print(stats)
            # R51 (ruling): без ненулевого кода возврата отказ (в т.ч. отказ
            # R47-порога — «подозрительно короткий ответ Overpass», самый
            # тихий и самый опасный случай) виден только человеку, читающему
            # stdout, а cron или скриптовый раннер его не увидит никогда —
            # ровно то, для чего stats["failed"] вообще заведён. Прецедент
            # в этом же файле — `eval --check` (см. выше): единственная
            # другая подкоманда, которая вообще сигналит отказ кодом
            # возврата, делает это через sys.exit(1). Здесь тот же путь,
            # но безусловно — под `metro` нет отдельного --check флага и
            # это не запрошено бриф-шаблоном, а stats["failed"] уже
            # ЯВЛЯЕТСЯ решением оператора «на этот запуск считать отказом».
            if stats["failed"]:
                sys.exit(1)
        elif args.cmd == "extract-windows":
            from habitus.clean.windows import extract_windows
            from habitus.online.llm import OpenRouterLLM
            print(extract_windows(conn, OpenRouterLLM(), limit=args.limit))


if __name__ == "__main__":
    main()
