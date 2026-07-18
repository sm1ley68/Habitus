# habitus/online/geo.py — Geo-Spatial Agent: изохроны и SQL-гео-предикаты
import json
import re
from dataclasses import dataclass
from typing import Protocol

import requests
from habitus.clean.geocode import geocode_address
from habitus.config import settings

WALK_SPEED_M_PER_MIN = 80.0        # пешеход ~4.8 км/ч


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


@dataclass
class AreaMatch:
    sql: str
    params: tuple
    label: str
    widen: list  # list[tuple[str, tuple, str]] — шире→шире, финал ("TRUE", (), «вся Москва»)


CARDINAL: dict[str, tuple[str, ...]] = {
    "N": ("САО", "СВАО", "СЗАО"), "S": ("ЮАО", "ЮВАО", "ЮЗАО"),
    "W": ("ЗАО", "СЗАО", "ЮЗАО"), "E": ("ВАО", "СВАО", "ЮВАО"),
    "NE": ("СВАО",), "NW": ("СЗАО",), "SE": ("ЮВАО",), "SW": ("ЮЗАО",),
    "C": ("ЦАО",),
}
_DROP = ("TRUE", (), "по всей Москве")


def _okrug_match(okrugs: tuple[str, ...], label: str) -> AreaMatch:
    return AreaMatch("okrug = ANY(%s)", (list(okrugs),), label, [_DROP])


def _cardinal_dirs(area: str) -> set[str] | None:
    """Множество направлений из фразы или None, если есть не-направление."""
    words = re.findall(r"[а-яёa-z]+", area.lower())
    dirs: set[str] = set()
    for w in words:
        if w in _AREA_FILLER:
            continue
        d = _dir_of(w)
        if d is None:
            return None
        dirs.add(d)
    return dirs or None


def _cardinal_match(area: str) -> AreaMatch | None:
    dirs = _cardinal_dirs(area)
    if dirs is None:
        return None
    if dirs == {"C"}:
        return _okrug_match(CARDINAL["C"], "центр (ЦАО)")
    # диагональ: пара сторона+сторона → точный округ
    diag = {frozenset({"N", "E"}): "NE", frozenset({"N", "W"}): "NW",
            frozenset({"S", "E"}): "SE", frozenset({"S", "W"}): "SW"}
    key = frozenset(dirs)
    if key in diag:
        code = diag[key]
        return _okrug_match(CARDINAL[code], f"{code}: округ {CARDINAL[code][0]}")
    if len(dirs) == 1:
        d = next(iter(dirs))
        return _okrug_match(CARDINAL[d], f"сторона света ({', '.join(CARDINAL[d])})")
    return None


def resolve_area(area: str, conn=None, *, geocoder=geocode_address) -> AreaMatch | None:
    """Область запроса → AreaMatch (SQL-предикат + цепочка расширения) или None."""
    if not area or not area.strip():
        return None
    card = _cardinal_match(area)
    if card is not None:
        return card
    return None   # ветки named/admin/ring/fallback — Tasks 5–6
