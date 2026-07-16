# habitus/online/geo.py — Geo-Spatial Agent: изохроны и SQL-гео-предикаты
import json
from typing import Protocol

import requests
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
