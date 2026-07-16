"""Grounded, query-specific object dossier construction.

Every hero block has an all-or-nothing boundary: when a required source is
missing the generic secondary block remains, but no synthetic zero/default is
published as an observed city fact.
"""

from __future__ import annotations

from dataclasses import dataclass
from functools import lru_cache
import json
import math
from typing import Callable

import requests
from psycopg.rows import dict_row

from habitus.clean.geocode import geocode_address
from habitus.online.geo import DirectionsProvider
from habitus.online.schema import (
    BriefItem, CompromiseNote, DirectLight, DossierPayload, DossierRequest,
    FamilyRoutingData, HouseholdMember, LifestyleBlock, LineStringGeometry,
    Obstruction, RelaxationNote, RouteLeg, SocialEnvironmentData, SocialScores,
    SunHoursBySeason, VerdictInfo, ViewClimateData,
)


ORIENTATION_DEG = {
    "N": 0.0, "NE": 45.0, "E": 90.0, "SE": 135.0,
    "S": 180.0, "SW": 225.0, "W": 270.0, "NW": 315.0,
}
ROUTE_PROFILE = {
    "walk": "foot-walking", "scooter": "cycling-regular",
    "car": "driving-car",
}
SEASON_DAY = {"winter": 355, "spring": 79, "summer": 172, "autumn": 266}
MSK_BOUNDS = (37.30, 55.48, 37.95, 55.95)


class DossierNotFound(LookupError):
    pass


@dataclass
class ListingEvidence:
    lon: float
    lat: float
    level: int | None
    levels: int | None
    facts: dict


class NASACloudinessProvider:
    """Cached NASA POWER climatology ratio for a coordinate cell."""

    def __init__(self, session=None):
        self._session = session or requests.Session()

    @lru_cache(maxsize=64)
    def cloudiness(self, lon_cell: float, lat_cell: float) -> float | None:
        try:
            response = self._session.get(
                "https://power.larc.nasa.gov/api/temporal/climatology/point",
                params={
                    "parameters": "ALLSKY_SFC_SW_DWN,CLRSKY_SFC_SW_DWN",
                    "community": "SB", "longitude": lon_cell,
                    "latitude": lat_cell, "format": "JSON",
                }, timeout=20)
            response.raise_for_status()
            params = response.json()["properties"]["parameter"]
            all_sky = float(params["ALLSKY_SFC_SW_DWN"]["ANN"])
            clear_sky = float(params["CLRSKY_SFC_SW_DWN"]["ANN"])
            if clear_sky <= 0:
                return None
            return round(max(0.0, min(1.0, 1.0 - all_sky / clear_sky)), 3)
        except (requests.RequestException, KeyError, TypeError, ValueError):
            return None

    def for_point(self, lon: float, lat: float) -> float | None:
        return self.cloudiness(round(lon, 1), round(lat, 1))


DEFAULT_CLIMATE_PROVIDER = NASACloudinessProvider()


def _inside_moscow(point: tuple[float, float]) -> bool:
    lon, lat = point
    west, south, east, north = MSK_BOUNDS
    return west <= lon <= east and south <= lat <= north


def _fetch_listing(conn, object_id: str) -> ListingEvidence:
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("""
            SELECT ST_X(geom) AS lon, ST_Y(geom) AS lat, level, levels,
                   walk_min_school, walk_min_metro, walk_min_park,
                   bar_density_500m, noise_level, window_orientation,
                   insolation_rough
            FROM listings WHERE external_id=%s AND geom IS NOT NULL
        """, (object_id,))
        row = cur.fetchone()
    if not row:
        raise DossierNotFound(object_id)
    facts = {key: row[key] for key in (
        "walk_min_school", "walk_min_metro", "walk_min_park",
        "bar_density_500m", "noise_level", "window_orientation",
        "insolation_rough",
    )}
    return ListingEvidence(float(row["lon"]), float(row["lat"]),
                           row["level"], row["levels"], facts)


def _orientation(facts: dict, requested: list[str]) -> float | None:
    values = [str(v).upper() for v in (facts.get("window_orientation") or [])
              if str(v).upper() in ORIENTATION_DEG]
    matches = [v for v in values if v in requested]
    selected = matches[0] if len(matches) == 1 else values[0] if len(values) == 1 else None
    return ORIENTATION_DEG.get(selected) if selected else None


def _minutes_to_clock(clock: str, delta: int) -> str:
    hour, minute = map(int, clock.split(":"))
    total = (hour * 60 + minute + delta) % (24 * 60)
    return f"{total // 60:02d}:{total % 60:02d}"


def _family_data(req: DossierRequest, listing: ListingEvidence,
                 route_provider: DirectionsProvider | None,
                 geocoder: Callable[[str], tuple[float, float] | None]) -> FamilyRoutingData | None:
    if route_provider is None or not req.parsed_query.household:
        return None
    members = []
    home = (listing.lon, listing.lat)
    for member_intent in req.parsed_query.household:
        legs = []
        start = home
        for intent in member_intent.legs:
            profile = ROUTE_PROFILE.get(intent.mode)
            if profile is None or (intent.depart is None and intent.arrive is None):
                continue
            target = geocoder(intent.to_label if "моск" in intent.to_label.lower()
                              else f"{intent.to_label}, Москва")
            if target is None or not _inside_moscow(target):
                continue
            try:
                geometry, seconds = route_provider.directions(start, target, profile)
                minutes = max(1, int(math.ceil(seconds / 60)))
                depart = intent.depart or _minutes_to_clock(intent.arrive, -minutes)
                arrive = intent.arrive or _minutes_to_clock(intent.depart, minutes)
                legs.append(RouteLeg(
                    to_label=intent.to_label, to_kind=intent.to_kind,
                    mode=intent.mode, depart=depart, arrive=arrive,
                    minutes=minutes, safety="caution",
                    geometry=LineStringGeometry.model_validate(geometry)))
                start = target
            except (requests.RequestException, KeyError, TypeError, ValueError):
                continue
        if legs:
            members.append(HouseholdMember(id=member_intent.id,
                                           label=member_intent.label, legs=legs))
    return FamilyRoutingData(home=home, members=members) if members else None


def _social_data(conn, listing: ListingEvidence) -> SocialEnvironmentData | None:
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("""
            WITH home AS (
                SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom
            ), ring AS (
                SELECT ST_Buffer(geom::geography, 500)::geometry AS geom FROM home
            )
            SELECT e.layer, e.weight,
                   ST_Area(ST_Intersection(e.geom, ring.geom)::geography) AS overlap_m2,
                   ST_AsGeoJSON(ST_Intersection(e.geom, ring.geom)) AS geometry
            FROM urban_evidence e, ring
            WHERE e.city='msk' AND e.layer IN ('communal','crime')
              AND ST_Intersects(e.geom, ring.geom)
        """, (listing.lon, listing.lat))
        rows = cur.fetchall()
    by_layer = {"communal": [], "crime": []}
    heat_features = []
    for row in rows:
        if not row["geometry"] or row["overlap_m2"] <= 0:
            continue
        by_layer[row["layer"]].append((float(row["weight"]), float(row["overlap_m2"])))
        heat_features.append({
            "type": "Feature",
            "properties": {"layer": row["layer"], "weight": float(row["weight"])},
            "geometry": json.loads(row["geometry"]),
        })
    if not by_layer["communal"] or not by_layer["crime"]:
        return None

    def weighted(values):
        denominator = sum(area for _, area in values)
        return sum(weight * area for weight, area in values) / denominator

    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("""
            WITH home AS (SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom)
            SELECT kind, COALESCE(name, '') AS name,
                   ST_X(p.geom) lon, ST_Y(p.geom) lat
            FROM poi p, home
            WHERE p.kind IN ('bar','alcohol')
              AND ST_DWithin(p.geom::geography, home.geom::geography, 500)
        """, (listing.lon, listing.lat))
        bars = cur.fetchall()
        current_bars = float(listing.facts.get("bar_density_500m") or len(bars))
        cur.execute("""
            SELECT CASE WHEN count(*)=0 THEN NULL ELSE
              count(*) FILTER (WHERE COALESCE(bar_density_500m,0) <= %s)::float / count(*) END
            AS percentile FROM listings WHERE is_active=TRUE
        """, (current_bars,))
        bar_percentile = cur.fetchone()["percentile"]
    if bar_percentile is None:
        return None
    pois = []
    for bar in bars:
        coords = [float(bar["lon"]), float(bar["lat"])]
        label = bar["name"]
        pois.append({"kind": "bar", "coordinates": coords,
                     "label": label or "Бар/алкомаркет"})
        heat_features.append({"type": "Feature",
                              "properties": {"layer": "bars", "weight": 1.0},
                              "geometry": {"type": "Point", "coordinates": coords}})
    return SocialEnvironmentData(
        home=(listing.lon, listing.lat), radius_m=500,
        scores=SocialScores(
            communal_share=round(weighted(by_layer["communal"]), 3),
            bars_density=round(float(bar_percentile), 3),
            crime_index=round(weighted(by_layer["crime"]), 3)),
        heat={"type": "FeatureCollection", "features": heat_features}, pois=pois)


def _obstructions(conn, listing: ListingEvidence) -> list[Obstruction]:
    observer_height = max(1, listing.level or 1) * 3.0
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("""
            WITH home AS (SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom)
            SELECT COALESCE(f.name, 'Соседнее здание') AS label,
                   degrees(ST_Azimuth(home.geom, ST_Centroid(f.geom))) AS azimuth,
                   f.height_m,
                   ST_Distance(home.geom::geography, ST_Centroid(f.geom)::geography) AS distance
            FROM urban_features f, home
            WHERE f.kind='building' AND f.height_m IS NOT NULL
              AND NOT ST_Contains(f.geom, home.geom)
              AND ST_DWithin(f.geom::geography, home.geom::geography, 200)
            ORDER BY distance LIMIT 24
        """, (listing.lon, listing.lat))
        rows = cur.fetchall()
    result = []
    for row in rows:
        distance = max(1.0, float(row["distance"]))
        elevation = math.degrees(math.atan2(max(0.0, float(row["height_m"]) - observer_height), distance))
        if elevation > 0:
            result.append(Obstruction(azimuth_deg=float(row["azimuth"]) % 360,
                                      elevation_deg=min(90.0, elevation),
                                      label=row["label"]))
    return result


def _angular_distance(a: float, b: float) -> float:
    return abs((a - b + 180) % 360 - 180)


def _solar_samples(lat: float, day: int, orientation: float,
                   obstructions: list[Obstruction]) -> list[float]:
    lat_r = math.radians(lat)
    decl = math.radians(23.44 * math.sin(2 * math.pi * (284 + day) / 365))
    lit = []
    for quarter in range(0, 96):
        hour = quarter / 4
        hour_angle = math.radians(15 * (hour - 12))
        sin_elev = (math.sin(lat_r) * math.sin(decl) +
                    math.cos(lat_r) * math.cos(decl) * math.cos(hour_angle))
        elevation = math.degrees(math.asin(max(-1.0, min(1.0, sin_elev))))
        if elevation <= 0:
            continue
        azimuth = (math.degrees(math.atan2(
            math.sin(hour_angle),
            math.cos(hour_angle) * math.sin(lat_r) - math.tan(decl) * math.cos(lat_r))) + 180) % 360
        if _angular_distance(azimuth, orientation) > 90:
            continue
        blocked = any(_angular_distance(azimuth, o.azimuth_deg) <= 8 and
                      o.elevation_deg >= elevation for o in obstructions)
        if not blocked:
            lit.append(hour)
    return lit


def _view_type(conn, listing: ListingEvidence, orientation: float) -> str:
    # A narrow, real-coordinate view corridor.  The classification is explicit
    # and deterministic; absent OSM features fall back to the neutral "street".
    radians = math.radians(orientation)
    end_lon = listing.lon + math.sin(radians) * 300 / (111_320 * math.cos(math.radians(listing.lat)))
    end_lat = listing.lat + math.cos(radians) * 300 / 111_320
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute("""
            WITH ray AS (SELECT ST_MakeLine(
              ST_SetSRID(ST_MakePoint(%s,%s),4326),
              ST_SetSRID(ST_MakePoint(%s,%s),4326)) AS geom),
            home AS (SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom)
            SELECT f.kind, ST_Distance(home.geom::geography, f.geom::geography) distance
            FROM urban_features f, ray, home
            WHERE ST_Intersects(f.geom, ST_Buffer(ray.geom::geography, 5)::geometry)
            ORDER BY distance LIMIT 1
        """, (listing.lon, listing.lat, end_lon, end_lat, listing.lon, listing.lat))
        first = cur.fetchone()
        cur.execute("""
            WITH home AS (SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom)
            SELECT count(*) AS total FROM urban_features f, home
            WHERE f.kind='building' AND NOT ST_Contains(f.geom, home.geom)
              AND ST_DWithin(f.geom::geography, home.geom::geography, 50)
        """, (listing.lon, listing.lat))
        surrounding = cur.fetchone()["total"]
    if surrounding >= 6:
        return "well"
    if first:
        if first["kind"] == "water":
            return "water"
        if first["kind"] == "park":
            return "courtyard_park"
        if first["kind"] == "building" and float(first["distance"]) <= 30:
            return "wall"
    return "street"


def _climate_data(conn, req: DossierRequest, listing: ListingEvidence,
                  climate_provider) -> ViewClimateData | None:
    orientation = _orientation(listing.facts, req.parsed_query.window_orientation)
    if orientation is None:
        return None
    with conn.cursor() as cur:
        cur.execute("""
            WITH home AS (SELECT ST_SetSRID(ST_MakePoint(%s,%s),4326) AS geom)
            SELECT avg(e.db) FROM urban_evidence e, home
            WHERE e.city='msk' AND e.layer='noise'
              AND ST_DWithin(e.geom::geography, home.geom::geography, 500)
        """, (listing.lon, listing.lat))
        db = cur.fetchone()[0]
    cloudiness = climate_provider.for_point(listing.lon, listing.lat)
    if db is None or cloudiness is None:
        return None
    obstructions = _obstructions(conn, listing)
    samples = {season: _solar_samples(listing.lat, day, orientation, obstructions)
               for season, day in SEASON_DAY.items()}
    summer = samples["summer"]
    if not summer:
        direct = DirectLight.model_validate({"from": "00:00", "to": "00:00"})
    else:
        direct = DirectLight.model_validate({
            "from": _minutes_to_clock("00:00", int(round(min(summer) * 60))),
            "to": _minutes_to_clock("00:00", int(round((max(summer) + .25) * 60))),
        })
    return ViewClimateData(
        orientation_deg=orientation, direct_light=direct,
        sun_hours_by_season=SunHoursBySeason(**{
            season: round(len(values) * .25, 2) for season, values in samples.items()}),
        cloudiness_factor=cloudiness, obstructions=obstructions,
        view_type=_view_type(conn, listing, orientation), db=float(db))


def _fact_num(facts: dict, key: str) -> float | None:
    value = facts.get(key)
    return float(value) if isinstance(value, (int, float)) and not isinstance(value, bool) else None


def _brief(req: DossierRequest, facts: dict) -> list[BriefItem]:
    result = []
    relaxed_text = " ".join(req.relaxed).lower()
    for geo in req.parsed_query.geo:
        value = _fact_num(facts, f"walk_min_{geo.kind}")
        label = f"{geo.kind}: не более {geo.walk_minutes} мин пешком"
        if value is None:
            status = "unknown"
        elif value <= geo.walk_minutes:
            status = "met"
        elif geo.kind in relaxed_text or "пешком" in relaxed_text:
            status = "relaxed"
        else:
            status = "compromise"
        result.append(BriefItem(label=label, status=status))
    if req.parsed_query.noise_max:
        noise = facts.get("noise_level")
        result.append(BriefItem(label="Тихое окружение",
                                status="met" if noise == "low" else
                                "unknown" if not noise else "compromise"))
    if "bars" in req.parsed_query.stop_factors:
        bars = _fact_num(facts, "bar_density_500m")
        result.append(BriefItem(label="Без баров и алкомаркетов рядом",
                                status="met" if bars == 0 else
                                "unknown" if bars is None else "compromise"))
    if req.parsed_query.window_orientation:
        orientation = _orientation(facts, req.parsed_query.window_orientation)
        result.append(BriefItem(label="Ориентация окон: " + ", ".join(req.parsed_query.window_orientation),
                                status="met" if orientation is not None else "unknown"))
    for member in req.parsed_query.household:
        for leg in member.legs:
            result.append(BriefItem(label=f"{member.label}: {leg.to_label}", status="unknown"))
    return result


def _secondary_blocks(facts: dict) -> list[LifestyleBlock]:
    blocks = []
    school = _fact_num(facts, "walk_min_school")
    metro = _fact_num(facts, "walk_min_metro")
    if school is not None or metro is not None:
        value = school if school is not None else metro
        score = "A" if value <= 10 else "B+" if value <= 15 else "B" if value <= 20 else "C"
        blocks.append(LifestyleBlock(
            key="logistics", title="Логистика и школы", icon="school", score=score,
            verdict_line="Проверена пешая доступность.",
            description=f"Ближайшая подтверждённая точка — {value:g} мин пешком.",
            metrics={"minutes": value}))
    bars = _fact_num(facts, "bar_density_500m")
    if bars is not None:
        blocks.append(LifestyleBlock(
            key="social_environment", title="Окружение", icon="users",
            score="A" if bars == 0 else "B" if bars <= 2 else "C",
            verdict_line="Доступен подтверждённый слой заведений.",
            description=f"{bars:g} баров/алкомаркетов в радиусе 500 м.",
            metrics={"bars_500m": bars}))
    if facts.get("window_orientation") or facts.get("noise_level"):
        blocks.append(LifestyleBlock(
            key="view_and_climate", title="Вид и климат", icon="sun", score="B",
            verdict_line="Часть климатических данных пока неполна.",
            description="Доступны только подтверждённые базовые характеристики окна и окружения."))
    return blocks


def build_dossier(req: DossierRequest, conn, *,
                  route_provider: DirectionsProvider | None = None,
                  geocoder=geocode_address, climate_provider=None) -> DossierPayload:
    listing = _fetch_listing(conn, req.object_id)
    brief = _brief(req, listing.facts)
    blocks = _secondary_blocks(listing.facts)
    sources = set()

    is_moscow = req.city == "msk"
    family = _family_data(req, listing, route_provider, geocoder) if is_moscow else None
    if family:
        blocks = [b for b in blocks if b.key != "logistics"]
        blocks.insert(0, LifestyleBlock(
            key="family_routing", tier="hero", title="Суточный ритм семьи",
            icon="route", score="A" if all(leg.safety == "safe" for m in family.members for leg in m.legs) else "B",
            verdict_line="Маршруты построены по дорожному графу.",
            description="Показаны только явно названные поездки и подтверждённые маршруты.", data=family))
        sources.add("route")
        routed = {(m.label, leg.to_label) for m in family.members for leg in m.legs}
        for item in brief:
            if any(member in item.label and destination in item.label
                   for member, destination in routed):
                item.status = "met"

    social = _social_data(conn, listing) if is_moscow else None
    if social:
        blocks = [b for b in blocks if b.key != "social_environment"]
        blocks.append(LifestyleBlock(
            key="social_environment", tier="hero", title="Социальное окружение",
            icon="users", score="A" if max(social.scores.model_dump().values()) < .34 else
            "B" if max(social.scores.model_dump().values()) < .67 else "C",
            verdict_line="Риски рассчитаны только по точным импортированным слоям.",
            description="Оценка в радиусе 500 м без подстановки отсутствующих данных.", data=social))
        sources.update({"communal", "bars", "crime"})

    climate_provider = climate_provider or DEFAULT_CLIMATE_PROVIDER
    climate = _climate_data(conn, req, listing, climate_provider) if is_moscow else None
    if climate:
        blocks = [b for b in blocks if b.key != "view_and_climate"]
        blocks.append(LifestyleBlock(
            key="view_and_climate", tier="hero", title="Вид и климат", icon="sun",
            score="A" if climate.db < 40 and climate.sun_hours_by_season.summer >= 5 else
            "B" if climate.db < 55 else "C",
            verdict_line="Свет рассчитан с учётом геометрии зданий и облачности.",
            description="Сезонная инсоляция, препятствия, тип вида и точный шум.", data=climate))
        sources.update({"solar", "noise"})

    compromises = [CompromiseNote(block_key="criteria", text=item.label)
                   for item in brief if item.status == "compromise"]
    verified = sum(item.status in {"met", "relaxed", "compromise"} for item in brief)
    confidence = verified / max(1, len(brief)) - .1 * len(set(req.degraded))
    confidence = round(max(0.0, min(1.0, confidence)), 2)
    met = next((item.label for item in brief if item.status == "met"), None)
    weak = next((item.label for item in brief if item.status in {"compromise", "relaxed"}), None)
    if met and weak:
        headline = f"Подходит по критерию «{met}» — есть компромисс по «{weak}»"
    elif met:
        headline = "Ключевые критерии подтверждены данными"
    else:
        headline = "Недостаточно данных для уверенного вердикта"
    zone_parts = [f"{g.walk_minutes}-мин доступность: {g.kind}" for g in req.parsed_query.geo]
    if req.relaxed:
        zone_parts.append("ослабления: " + "; ".join(req.relaxed))
    return DossierPayload(
        verdict=VerdictInfo(headline=headline, confidence=confidence,
                            layers_checked=len(sources)),
        brief=brief, blocks=blocks, compromises=compromises,
        relaxation=[RelaxationNote(text=text) for text in req.relaxed],
        zone_rationale="; ".join(zone_parts))
