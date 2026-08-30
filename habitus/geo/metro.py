# habitus/geo/metro.py — рельсовый транспорт из OSM: метро, МЦК, МЦД.
#
# Точные Overpass-фильтры взяты из разведки, запротоколированной в
# docs/notes/osm-transit-tags-2026-08-29.md. Разметка у МЦК и МЦД менялась,
# поэтому фиксировать её по памяти нельзя: при расхождении правится ФИЛЬТР,
# модель данных к конкретным тегам не привязана.
import json
import re
import time
from dataclasses import dataclass, field

import psycopg
import requests

from habitus.geo.osm_extract import HEADERS, OVERPASS_URL, RETRY_STATUS, TRANSIT_AREA

SYSTEMS = ("subway", "mck", "mcd")

# Значения подтверждены живым Overpass — протокол разведки,
# docs/notes/osm-transit-tags-2026-08-29.md, раздел «Вывод».
# МЦК размечено как route=train (НЕ light_rail — эта гипотеза дала 0
# элементов), различитель от обычного метро — ref="14", официальный номер
# линии МЦК в Мосметро. МЦД — тоже route=train, различитель — network
# (категориальный тег, устойчивее к переименованиям, чем текст в name).
TRANSIT_RELATION_FILTER = {
    "subway": 'relation["route"="subway"]',
    "mck":    'relation["route"="train"]["ref"="14"]',
    "mcd":    'relation["route"="train"]["network"~"МЦД|MCD"]',
}

# Роли members, которыми в OSM размечена остановка. Сравниваем ПРЕФИКСОМ,
# а не точным равенством: конечные станции линии в PTv2 размечены ролями
# stop_entry_only/stop_exit_only, а не голым "stop" — точное сравнение это
# теряет, то есть теряет ОБА конца КАЖДОЙ линии молча (relation 305810 в
# фикстуре: 27 node-members, первый и последний — как раз entry/exit-only).
# Безымянная станция ниже по коду всё равно отсеивается отдельной проверкой,
# так что расширение матчинга безопасно.
#
# platform — запасной вариант НА УРОВЕНЬ РЕЛЕЙШЕНА: у части линий проставлен
# только он, но внутри ОДНОГО релейшена берём platform*-узлы только если там
# нет вообще ни одного stop*-узла. Иначе одна физическая станция, размеченная
# и как stop, и как platform (mixed relation — в подземке этого не видно, но
# на МЦК/МЦД node-платформы плюс node-stop в одном релейшене правдоподобны),
# задвоилась бы в списке станций и дала бы нулевую по длине связь в графе.


@dataclass
class StationRaw:
    osm_id: int
    name: str
    lon: float
    lat: float


@dataclass
class LineRaw:
    system: str
    ref: str
    name: str
    colour: str | None
    stations: list[StationRaw] = field(default_factory=list)
    geometry: list[list[float]] = field(default_factory=list)
    # Кольцевая линия (МЦК, Кольцевая линия метро): первая station-нода
    # релейшена в OSM обычно повторена последней, чтобы замкнуть кольцо на
    # схеме. Дедуп в parse_route_relations по нормализованному имени снимает
    # этот повтор из stations — то есть после парсинга по списку станций
    # кольцо от обычной линии уже не отличить. Флаг вычисляется ДО дедупа, по
    # сырой последовательности members, и переживает дедуп именно для этого:
    # Задача 6 замыкает граф явным ребром последняя→первая только когда флаг
    # True (см. task-6-brief, R24). Никогда не выводится из ref (никаких
    # хардкодов вида ref == "14") — только из данных.
    ring: bool = False


def normalize_station_name(name: str) -> str:
    """Ключ сопоставления станции с курируемым JSON.

    Ключ по ИМЕНИ, а не по osm_id: правка разметки в OSM не должна обнулять
    курированные времена. Схлопываем регистр, ё→е, повторные пробелы и
    кавычки — ровно те различия, которыми одна и та же станция пишется
    по-разному в OSM и в курируемом файле.
    """
    s = name.strip().lower().replace("ё", "е")
    s = s.replace("«", "").replace("»", "").replace('"', "")
    return re.sub(r"\s+", " ", s)


def parse_route_relations(payload: dict, system: str) -> list[LineRaw]:
    """Ответ Overpass (`out body; >; out body geom;`) → линии со станциями
    в порядке следования."""
    nodes = {e["id"]: e for e in payload.get("elements", []) if e.get("type") == "node"}
    ways = {e["id"]: e for e in payload.get("elements", []) if e.get("type") == "way"}
    lines: list[LineRaw] = []

    for el in payload.get("elements", []):
        if el.get("type") != "relation":
            continue
        tags = el.get("tags") or {}
        members = el.get("members", [])

        # На уровне релейшена: если есть хоть один stop*-узел — platform*
        # игнорируем целиком (см. комментарий у платформы/стопа выше).
        has_stop_node = any(
            m.get("type") == "node" and (m.get("role") or "").startswith("stop")
            for m in members
        )

        stations: list[StationRaw] = []
        seen_ids: set[int] = set()
        seen_names: set[str] = set()
        geometry: list[list[float]] = []
        # Сырая (до дедупа) последовательность нормализованных имён
        # station-членов — только по ней определяется ring (см. комментарий
        # у поля LineRaw.ring).
        raw_names: list[str] = []

        for m in members:
            role = m.get("role") or ""
            is_stop_member = (role.startswith("stop") if has_stop_node
                              else role.startswith("platform"))
            if m.get("type") == "node" and is_stop_member:
                node = nodes.get(m["ref"])
                if node is None:
                    continue
                nm = (node.get("tags") or {}).get("name")
                # Безымянная станция — мусор: подписать её на схеме нечем.
                if not nm:
                    continue
                norm = normalize_station_name(nm)
                raw_names.append(norm)
                # Дедуп по osm_id (одна и та же нода дважды в members) И по
                # нормализованному имени (одна физическая станция как
                # stop-нода и platform-нода под разными osm_id — тот же
                # частный случай, что и mixed-релейшен, но без общего id).
                if node["id"] in seen_ids or norm in seen_names:
                    continue
                seen_ids.add(node["id"])
                seen_names.add(norm)
                stations.append(StationRaw(osm_id=node["id"], name=nm,
                                           lon=node["lon"], lat=node["lat"]))
            elif m.get("type") == "way" and not m.get("role"):
                way = ways.get(m["ref"])
                for p in (way or {}).get("geometry") or []:
                    geometry.append([p["lon"], p["lat"]])

        # Релейшен без остановок описывает трассу, а не маршрут — линией он быть
        # не может: ни одной станции для графа из него не извлечь.
        if len(stations) < 2:
            continue
        is_ring = len(raw_names) >= 2 and raw_names[0] == raw_names[-1]
        lines.append(LineRaw(
            system=system,
            ref=tags.get("ref") or tags.get("name") or str(el["id"]),
            name=tags.get("name") or tags.get("ref") or str(el["id"]),
            colour=tags.get("colour"),
            stations=stations, geometry=geometry, ring=is_ring))
    return lines


def fetch_system(system: str, city: str, http_post=requests.post,
                 retries: int = 4, backoff: float = 3.0) -> list[LineRaw]:
    """Линии одной системы для города. Ретраи — как у fetch_kind: публичный
    Overpass под нагрузкой регулярно отдаёт транзиентные 429/502/503/504.

    Запрос обязательно рекурсивный (`out body; >; out body geom;`), а не
    просто `out body geom;`. Без `>;` node-members релейшена приезжают с
    координатами, но БЕЗ tags — то есть без name станции. Парсер отбрасывает
    безымянные станции (см. parse_route_relations), поэтому «out body geom»
    молча даёт ноль станций на линию: запрос отработает без единой ошибки,
    а граф просто не соберётся. Рекурсия `>;` дотягивает полные node/way с
    тегами — это подтверждено живым Overpass, см. протокол разведки,
    docs/notes/osm-transit-tags-2026-08-29.md, шаг 3.
    """
    q = f"[out:json][timeout:300];{TRANSIT_RELATION_FILTER[system]}{TRANSIT_AREA[city]};out body;>;out body geom;"
    last = ""
    for attempt in range(retries):
        try:
            r = http_post(OVERPASS_URL, data={"data": q}, headers=HEADERS,
                          timeout=360)
            if r.status_code in RETRY_STATUS:
                last = f"HTTP {r.status_code}"
            else:
                r.raise_for_status()
                return parse_route_relations(r.json(), system)
        except requests.exceptions.RequestException as e:
            last = f"{type(e).__name__}: {e}"
        time.sleep(backoff * (attempt + 1))
    raise RuntimeError(f"Overpass '{system}/{city}' не удался за {retries} "
                       f"попыток: {last}")


def upsert_transit(lines: list[LineRaw], conn: psycopg.Connection, city: str,
                   curated: "CuratedTimes | None" = None) -> dict[str, int]:
    """Линии из OSM + курируемые времена → граф в БД. Идемпотентно.

    Импорт habitus.geo.metro_times — намеренно ВНУТРИ функции, а не на уровне
    модуля: metro_times импортирует normalize_station_name ИЗ этого модуля,
    и импорт на уровне модуля здесь дал бы цикл (task-6-brief, R2). Аннотация
    типа curated — строковая по той же причине: для неё импорт CuratedTimes
    вообще не нужен, даже внутри функции.
    """
    from habitus.geo.metro_times import (DEFAULT_SPEED_KMH, edge_seconds,
                                         headway_seconds, load_curated,
                                         transfer_seconds)

    cur_times = curated if curated is not None else load_curated(city)
    stats = {"lines": 0, "stations": 0, "edges": 0, "transfers": 0}

    with conn.cursor() as cur:
        for line in lines:
            # Интервал — ТОЛЬКО через headway_seconds(): она же несёт признак
            # оценки, который тут же пишется в headway_estimated. Заводить
            # локальный словарь дефолтов по системе запрещено ровно затем,
            # чтобы не было двух копий этих чисел в двух модулях (R29/R30).
            # Та же логика применена к скорости: DEFAULT_SPEED_KMH — общий с
            # metro_times.edge_seconds() источник дефолта, а не вторая копия.
            headway_s, headway_estimated = headway_seconds(
                cur_times, line.ref, line.system)
            fallback_speed_kmh = (cur_times.speeds.get(line.ref)
                                  or DEFAULT_SPEED_KMH[line.system])

            cur.execute("""
                INSERT INTO metro_line (city, system, ref, name, colour,
                                        headway_s, headway_estimated,
                                        fallback_speed_kmh)
                VALUES (%s,%s,%s,%s,%s,%s,%s,%s)
                ON CONFLICT (city, system, ref) DO UPDATE SET
                    name = EXCLUDED.name, colour = EXCLUDED.colour,
                    headway_s = EXCLUDED.headway_s,
                    headway_estimated = EXCLUDED.headway_estimated,
                    fallback_speed_kmh = EXCLUDED.fallback_speed_kmh,
                    updated_at = now()
                RETURNING id;""",
                (city, line.system, line.ref, line.name, line.colour,
                 headway_s, headway_estimated, fallback_speed_kmh))
            line_id = cur.fetchone()[0]
            stats["lines"] += 1

            # Перестраиваем станции линии целиком: порядок следования мог
            # измениться (продлили ветку в начало), а order_index — часть
            # уникального ключа. Каскад снимет старые рёбра этой линии.
            cur.execute("DELETE FROM metro_station WHERE line_id = %s;", (line_id,))
            ids: list[int] = []
            for i, st in enumerate(line.stations):
                cur.execute("""
                    INSERT INTO metro_station (city, line_id, osm_id, name,
                                               name_norm, geom, order_index)
                    VALUES (%s,%s,%s,%s,%s,
                            ST_SetSRID(ST_MakePoint(%s,%s),4326),%s)
                    RETURNING id;""",
                    (city, line_id, st.osm_id, st.name,
                     normalize_station_name(st.name), st.lon, st.lat, i))
                ids.append(cur.fetchone()[0])
                stats["stations"] += 1

            if len(line.geometry) >= 2:
                cur.execute("""
                    INSERT INTO metro_line_geom (line_id, geom)
                    VALUES (%s, ST_SetSRID(ST_GeomFromGeoJSON(%s),4326))
                    ON CONFLICT (line_id) DO UPDATE SET geom = EXCLUDED.geom;""",
                    (line_id, json.dumps({"type": "LineString",
                                          "coordinates": line.geometry})))

            # Последовательные перегоны линии плюс, для кольца, замыкающее
            # последняя→первая. Дедуп в parse_route_relations снимает повтор
            # первой станции в конце ring-маршрута (см. LineRaw.ring) — без
            # этого явного ребра стык кольца недостижим напрямую. Флаг ring
            # никогда не выводится здесь из ref — он уже вычислен в парсере.
            pairs = [(i, i + 1) for i in range(len(ids) - 1)]
            # >= 3, не >= 2: у двухстанционного кольца замыкающая пара
            # (последняя, первая) совпадает с уже учтённой (0, 1) — это одна
            # и та же связь, а не две (controller ruling R35, фикс-раунд 1).
            if line.ring and len(ids) >= 3:
                pairs.append((len(ids) - 1, 0))

            for i, j in pairs:
                a, b = line.stations[i], line.stations[j]
                cur.execute("""SELECT ST_Distance(
                                  (SELECT geom FROM metro_station WHERE id=%s)::geography,
                                  (SELECT geom FROM metro_station WHERE id=%s)::geography);""",
                            (ids[i], ids[j]))
                metres = float(cur.fetchone()[0])
                seconds, estimated = edge_seconds(cur_times, line.ref, a.name,
                                                  b.name, line.system, metres)
                for x, y in ((ids[i], ids[j]), (ids[j], ids[i])):
                    cur.execute("""
                        INSERT INTO metro_edge (city, from_station, to_station,
                                                seconds, estimated)
                        VALUES (%s,%s,%s,%s,%s)
                        ON CONFLICT (from_station, to_station) DO UPDATE SET
                            seconds = EXCLUDED.seconds,
                            estimated = EXCLUDED.estimated;""",
                        (city, x, y, seconds, estimated))
                    stats["edges"] += 1

        # Пересадка — два источника, объединённых (controller ruling R33,
        # фикс-раунд 1):
        #  1) одноимённые платформы на разных линиях — типовой случай, когда
        #     одна физическая станция размечена в OSM отдельным узлом на
        #     каждую линию под одним и тем же именем;
        #  2) курируемые пары РАЗНОИМЁННЫХ станций одного пересадочного узла
        #     («Охотный Ряд» ↔ «Театральная», «Площадь Гагарина» ↔ «Ленинский
        #     проспект» — оба реальных перехода в data/metro/msk.json именно
        #     такие). Self-join по name_norm их не находит НИКОГДА: имена не
        #     совпадают по определению. Без этой ветки курируемый transfers
        #     был мёртвым кодом на реальных данных — ни один курируемый переход,
        #     включая единственный outdoor=True во всём проекте, не попадал в
        #     БД (см. R27/R32 — та самая уличная МЦД↔метро пересадка).
        def _upsert_transfer(a_id: int, b_id: int, seconds: int,
                             estimated: bool, outdoor: bool) -> None:
            # outdoor приходит из transfer_seconds() как есть и пишется без
            # переинтерпретации: FALSE здесь означает ровно то, что говорит
            # курируемый источник — либо явное "не уличный", либо (вместе с
            # estimated=True) "не курировано, факт неизвестен", а не "мы не
            # проверяли, но пишем как факт" (R32). МЦД↔метро уличные
            # пересадки намеренно не курируются (R27) — эта функция их не
            # "дособирает" и не изобретает outdoor=True.
            cur.execute("""
                INSERT INTO metro_transfer (city, from_station, to_station,
                                            seconds, estimated, outdoor)
                VALUES (%s,%s,%s,%s,%s,%s)
                ON CONFLICT (from_station, to_station) DO UPDATE SET
                    seconds = EXCLUDED.seconds, estimated = EXCLUDED.estimated,
                    outdoor = EXCLUDED.outdoor;""",
                (city, a_id, b_id, seconds, estimated, outdoor))
            stats["transfers"] += 1

        cur.execute("""
            SELECT a.id, b.id, a.name, b.name
            FROM metro_station a JOIN metro_station b
              ON a.name_norm = b.name_norm AND a.line_id <> b.line_id
            WHERE a.city = %s AND b.city = %s;""", (city, city))
        for a_id, b_id, a_name, b_name in cur.fetchall():
            seconds, estimated, outdoor = transfer_seconds(cur_times, a_name, b_name)
            _upsert_transfer(a_id, b_id, seconds, estimated, outdoor)

        # Курируемые пары разноимённых станций. Ключ cur_times.transfers —
        # уже нормализованная пара имён (metro_times._pair сортирует их при
        # загрузке JSON), поэтому резолвим каждую сторону в id станций этого
        # города напрямую по name_norm — без повторной нормализации. Несколько
        # физических узлов с одним и тем же именем на разных линиях дают
        # декартово произведение пар, кроме пар на одной линии (это был бы
        # перегон между соседними станциями, а не пересадка — уже покрыт
        # рёбрами выше). Имя, которого нет среди станций города, просто не
        # даёт ни одной строки: станцию или пересадку это не изобретает.
        for norm_a, norm_b in cur_times.transfers:
            cur.execute("SELECT id, line_id FROM metro_station "
                       "WHERE city = %s AND name_norm = %s;", (city, norm_a))
            a_rows = cur.fetchall()
            cur.execute("SELECT id, line_id FROM metro_station "
                       "WHERE city = %s AND name_norm = %s;", (city, norm_b))
            b_rows = cur.fetchall()
            if not a_rows or not b_rows:
                continue
            # Имена уже нормализованы — transfer_seconds() лишь ищет по ним
            # ту же пару в cur_times.transfers/outdoor, повторная нормализация
            # normalize_station_name на нормализованной строке идемпотентна.
            seconds, estimated, outdoor = transfer_seconds(cur_times, norm_a, norm_b)
            for a_id, a_line in a_rows:
                for b_id, b_line in b_rows:
                    if a_line == b_line:
                        continue
                    _upsert_transfer(a_id, b_id, seconds, estimated, outdoor)
                    _upsert_transfer(b_id, a_id, seconds, estimated, outdoor)

    conn.commit()
    return stats
