# habitus/online/geo.py — Geo-Spatial Agent: изохроны и SQL-гео-предикаты
import json
import re
from typing import Protocol

import requests
from habitus.clean.geocode import geocode_address
from habitus.config import settings

WALK_SPEED_M_PER_MIN = 80.0        # пешеход ~4.8 км/ч
MOSCOW_CENTER = (37.6176, 55.7558)  # (lon, lat) — Кремль, точка отсчёта сторон
AREA_RADIUS_M = 3000.0              # именованное место → окрестность ~3 км
CENTER_RADIUS_M = 4000.0            # «центр» → круг вокруг Кремля


class IsochroneProvider(Protocol):
    def isochrone(self, lon: float, lat: float, minutes: int,
                  mode: str = "foot-walking") -> dict: ...


class DirectionsProvider(Protocol):
    def directions(self, start: tuple[float, float], end: tuple[float, float],
                   mode: str = "foot-walking") -> tuple[dict, float]: ...


class ORSProvider:
    """Реальный клиент OpenRouteService/Valhalla-совместимого API."""

    def __init__(self, session=None):
        self._session = session or requests.Session()

    def isochrone(self, lon: float, lat: float, minutes: int,
                  mode: str = "foot-walking") -> dict:
        resp = self._session.post(
            f"{settings.ors_base_url}/v2/isochrones/{mode}",
            json={"locations": [[lon, lat]], "range": [minutes * 60],
                  "range_type": "time"},
            headers={"Authorization": settings.ors_api_key},
            timeout=15)
        resp.raise_for_status()
        return resp.json()["features"][0]["geometry"]

    def directions(self, start: tuple[float, float], end: tuple[float, float],
                   mode: str = "foot-walking") -> tuple[dict, float]:
        """Return an explicit GeoJSON LineString and duration in seconds.

        The public ORS directions endpoint does not provide dependable public
        transport routing, so callers deliberately map only walk/scooter/car.
        """
        resp = self._session.post(
            f"{settings.ors_base_url}/v2/directions/{mode}/geojson",
            json={"coordinates": [list(start), list(end)],
                  "extra_info": ["waytype"]},
            headers={"Authorization": settings.ors_api_key},
            timeout=20)
        resp.raise_for_status()
        feature = resp.json()["features"][0]
        return feature["geometry"], float(feature["properties"]["summary"]["duration"])


def midpoint(a: tuple[float, float], b: tuple[float, float]) -> tuple[float, float]:
    """Центральная точка компромисса («работа в Сколково ↔ офис в Сити»)."""
    return ((a[0] + b[0]) / 2, (a[1] + b[1]) / 2)


def point_predicate(lon: float, lat: float, minutes: int,
                    provider: IsochroneProvider | None = None,
                    mode: str = "foot-walking") -> tuple[str, tuple]:
    """SQL-предикат гео-фильтра для build_where(extra_sql=..., extra_params=...).
    Без провайдера — Precomputed-путь: круг по прямой (без сети).
    С провайдером — честный изохрон-полигон с учётом режима передвижения."""
    if provider is None:
        radius_m = minutes * WALK_SPEED_M_PER_MIN
        return ("ST_DWithin(geom::geography, "
                "ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
                (lon, lat, radius_m))
    poly = provider.isochrone(lon, lat, minutes, mode)
    return ("ST_Within(geom, ST_SetSRID(ST_GeomFromGeoJSON(%s),4326))",
            (json.dumps(poly),))


# --- разбор области поиска: сторона города или именованное место ---
# Стемы направлений (совпадение по началу слова, чтобы ловить «северный»,
# «восточная» и т.п.). «юго»/«юг» → S, поэтому «юго-запад» = S+W.
_DIR_STEMS = [
    ("север", "N"), ("north", "N"),
    ("юго", "S"), ("юж", "S"), ("юг", "S"), ("south", "S"),
    ("запад", "W"), ("west", "W"),
    ("восточ", "E"), ("восток", "E"), ("east", "E"),
    ("центр", "C"), ("central", "C"), ("center", "C"), ("downtown", "C"),
]
# служебные слова, которые не мешают распознать чистое направление
_AREA_FILLER = {"на", "в", "во", "к", "москва", "москвы", "москве", "город",
                "города", "городе", "часть", "части", "район", "районе",
                "районы", "округ", "округа", "of", "the", "moscow"}


def _dir_of(word: str) -> str | None:
    for stem, d in _DIR_STEMS:
        if word.startswith(stem):
            return d
    return None


def _cardinal_predicate(area: str) -> tuple[str, tuple] | None:
    """Область → bbox по сторонам света ОТНОСИТЕЛЬНО центра. Возвращает None,
    если во фразе есть слово, не являющееся направлением (тогда это топоним →
    геокод). Так «Северное Бутово» (юг!) не примут за «север»."""
    words = re.findall(r"[а-яёa-z]+", area.lower())
    dirs: set[str] = set()
    for w in words:
        if w in _AREA_FILLER:
            continue
        d = _dir_of(w)
        if d is None:
            return None                      # непонятное слово → не кардинал
        dirs.add(d)
    if not dirs:
        return None
    lon0, lat0 = MOSCOW_CENTER
    if dirs == {"C"}:
        return ("ST_DWithin(geom::geography, "
                "ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
                (lon0, lat0, CENTER_RADIUS_M))
    preds: list[str] = []
    params: list = []
    if "N" in dirs: preds.append("ST_Y(geom) >= %s"); params.append(lat0)
    if "S" in dirs: preds.append("ST_Y(geom) <= %s"); params.append(lat0)
    if "W" in dirs: preds.append("ST_X(geom) <= %s"); params.append(lon0)
    if "E" in dirs: preds.append("ST_X(geom) >= %s"); params.append(lon0)
    if not preds:                            # только «центр» вместе с другими — уже выше
        return None
    return (" AND ".join(preds), tuple(params))


def resolve_area(area: str, *, geocoder=geocode_address) -> tuple[str, tuple] | None:
    """«север»/«юго-запад»/«центр» → bbox-предикат по сторонам света;
    именованное место («Сколково», «Патриаршие») → геокод в точку + окрестность.
    None, если геокодер не нашёл место (гео-фильтр просто не применяется)."""
    if not area or not area.strip():
        return None
    card = _cardinal_predicate(area)
    if card is not None:
        return card
    query = area if re.search(r"москв", area, re.I) else f"{area}, Москва"
    coords = geocoder(query)
    if not coords:
        return None
    lon, lat = coords
    return ("ST_DWithin(geom::geography, "
            "ST_SetSRID(ST_MakePoint(%s,%s),4326)::geography, %s)",
            (lon, lat, AREA_RADIUS_M))
