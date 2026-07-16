"""Strict import boundary for exact urban-risk and acoustic evidence."""

from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path
from typing import Any

import psycopg


class EvidenceValidationError(ValueError):
    pass


RISK_GEOMETRIES = {"Polygon", "MultiPolygon"}
NOISE_GEOMETRIES = {
    "Point", "MultiPoint", "LineString", "MultiLineString",
    "Polygon", "MultiPolygon",
}
RESERVED = {
    "source_id", "source", "city", "layer", "weight", "db", "observed_at"
}


def _validate_position(position: Any) -> None:
    if not isinstance(position, list) or len(position) != 2:
        raise EvidenceValidationError("every coordinate must be [lng, lat]")
    lon, lat = position
    if (not isinstance(lon, (int, float)) or isinstance(lon, bool)
            or not isinstance(lat, (int, float)) or isinstance(lat, bool)):
        raise EvidenceValidationError("coordinates must be numeric [lng, lat]")
    if not -180 <= lon <= 180 or not -90 <= lat <= 90:
        raise EvidenceValidationError("coordinates must be EPSG:4326 [lng, lat]")


def _validate_coordinates(geometry_type: str, coordinates: Any) -> None:
    depths = {
        "Point": 0, "MultiPoint": 1, "LineString": 1,
        "MultiLineString": 2, "Polygon": 2, "MultiPolygon": 3,
    }
    depth = depths[geometry_type]

    def walk(value: Any, remaining: int) -> None:
        if remaining == 0:
            _validate_position(value)
            return
        if not isinstance(value, list) or not value:
            raise EvidenceValidationError("geometry coordinates must not be empty")
        for child in value:
            walk(child, remaining - 1)

    walk(coordinates, depth)
    if geometry_type in {"LineString", "MultiLineString"}:
        lines = [coordinates] if geometry_type == "LineString" else coordinates
        if any(len(line) < 2 for line in lines):
            raise EvidenceValidationError("LineString requires at least two positions")
    if geometry_type in {"Polygon", "MultiPolygon"}:
        polygons = [coordinates] if geometry_type == "Polygon" else coordinates
        for polygon in polygons:
            for ring in polygon:
                if len(ring) < 4 or ring[0] != ring[-1]:
                    raise EvidenceValidationError("polygon rings must be closed")


def validate_feature(feature: dict[str, Any]) -> dict[str, Any]:
    if feature.get("type") != "Feature":
        raise EvidenceValidationError("every item must be a GeoJSON Feature")
    props = feature.get("properties") or {}
    geom = feature.get("geometry") or {}
    required = ("source_id", "source", "city", "layer", "observed_at")
    missing = [key for key in required if not props.get(key)]
    if missing:
        raise EvidenceValidationError("missing properties: " + ", ".join(missing))
    for key in ("source_id", "source"):
        if not isinstance(props[key], str) or not props[key].strip():
            raise EvidenceValidationError(f"{key} must be a non-empty string")
    try:
        observed_at = datetime.fromisoformat(str(props["observed_at"]).replace("Z", "+00:00"))
    except ValueError as exc:
        raise EvidenceValidationError("observed_at must be an ISO-8601 timestamp") from exc
    if observed_at.tzinfo is None:
        raise EvidenceValidationError("observed_at must include a timezone")
    if props["city"] != "msk":
        raise EvidenceValidationError("this importer currently accepts city=msk only")
    layer = props["layer"]
    geometry_type = geom.get("type")
    if layer in {"communal", "crime"}:
        if geometry_type not in RISK_GEOMETRIES:
            raise EvidenceValidationError(f"{layer} requires Polygon/MultiPolygon")
        weight = props.get("weight")
        if not isinstance(weight, (int, float)) or isinstance(weight, bool) or not 0 <= weight <= 1:
            raise EvidenceValidationError(f"{layer}.weight must be between 0 and 1")
        db = None
    elif layer == "noise":
        if geometry_type not in NOISE_GEOMETRIES:
            raise EvidenceValidationError("noise geometry is unsupported")
        db = props.get("db")
        if not isinstance(db, (int, float)) or isinstance(db, bool) or db < 0:
            raise EvidenceValidationError("noise.db must be a non-negative number")
        weight = None
    else:
        raise EvidenceValidationError("layer must be communal, crime or noise")
    if "coordinates" not in geom:
        raise EvidenceValidationError("geometry.coordinates is required")
    _validate_coordinates(geometry_type, geom["coordinates"])
    return {
        "source_id": str(props["source_id"]),
        "source": str(props["source"]),
        "city": "msk",
        "layer": layer,
        "geometry": json.dumps(geom, ensure_ascii=False),
        "weight": weight,
        "db": db,
        "observed_at": observed_at,
        "metadata": json.dumps({k: v for k, v in props.items() if k not in RESERVED},
                               ensure_ascii=False),
    }


def import_feature_collection(payload: dict[str, Any], conn: psycopg.Connection) -> int:
    if payload.get("type") != "FeatureCollection":
        raise EvidenceValidationError("root must be a GeoJSON FeatureCollection")
    rows = [validate_feature(feature) for feature in payload.get("features", [])]
    sql = """
        INSERT INTO urban_evidence
            (source_id, source, city, layer, geom, weight, db, observed_at, metadata)
        VALUES
            (%(source_id)s, %(source)s, %(city)s, %(layer)s,
             ST_SetSRID(ST_GeomFromGeoJSON(%(geometry)s), 4326),
             %(weight)s, %(db)s, %(observed_at)s, %(metadata)s::jsonb)
        ON CONFLICT (source, source_id, layer) DO UPDATE SET
            city=EXCLUDED.city, geom=EXCLUDED.geom, weight=EXCLUDED.weight,
            db=EXCLUDED.db, observed_at=EXCLUDED.observed_at,
            metadata=EXCLUDED.metadata, updated_at=now();
    """
    if rows:
        with conn.cursor() as cur:
            cur.executemany(sql, rows)
    conn.commit()
    return len(rows)


def import_geojson_file(path: Path, conn: psycopg.Connection) -> int:
    return import_feature_collection(json.loads(path.read_text(encoding="utf-8")), conn)
