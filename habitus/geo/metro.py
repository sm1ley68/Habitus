# habitus/geo/metro.py — рельсовый транспорт из OSM: метро, МЦК, МЦД.
#
# Точные Overpass-фильтры взяты из разведки, запротоколированной в
# docs/notes/osm-transit-tags-2026-08-29.md. Разметка у МЦК и МЦД менялась,
# поэтому фиксировать её по памяти нельзя: при расхождении правится ФИЛЬТР,
# модель данных к конкретным тегам не привязана.
import re
import time
from dataclasses import dataclass, field

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
        lines.append(LineRaw(
            system=system,
            ref=tags.get("ref") or tags.get("name") or str(el["id"]),
            name=tags.get("name") or tags.get("ref") or str(el["id"]),
            colour=tags.get("colour"),
            stations=stations, geometry=geometry))
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
