# habitus/online/schema.py — единственный источник правды по формам данных online-фазы
from typing import Annotated, Any, Literal
from pydantic import AfterValidator, BaseModel, Field, field_validator


BriefStatus = Literal["met", "compromise", "relaxed", "unknown"]
BlockTier = Literal["hero", "secondary"]
LifestyleIcon = Literal[
    "school", "users", "sun", "volume", "leaf", "hospital", "route"
]
Grade = Literal["A+", "A", "A-", "B+", "B", "B-", "C+", "C", "C-", "D"]
DestinationKind = Literal["school", "metro", "work", "park", "poi"]
TravelMode = Literal["walk", "scooter", "bus", "car", "metro"]
LegSafety = Literal["safe", "caution"]
SocialLayer = Literal["communal", "bars", "crime"]
ViewType = Literal["courtyard_park", "street", "water", "wall", "well"]


def _lng_lat(value: tuple[float, float]) -> tuple[float, float]:
    lon, lat = value
    if not -180 <= lon <= 180 or not -90 <= lat <= 90:
        raise ValueError("coordinates must be [lng, lat] in EPSG:4326")
    return value


LngLat = Annotated[tuple[float, float], AfterValidator(_lng_lat)]


class GeoConstraint(BaseModel):
    kind: Literal["school", "metro", "park"]
    walk_minutes: int          # порог пешей доступности


class HouseholdLegIntent(BaseModel):
    to_label: str = Field(min_length=1, max_length=200)
    to_kind: DestinationKind
    mode: TravelMode
    depart: str | None = None
    arrive: str | None = None

    @field_validator("depart", "arrive")
    @classmethod
    def valid_clock(cls, value: str | None) -> str | None:
        if value is None:
            return None
        parts = value.split(":")
        if len(parts) != 2 or not all(p.isdigit() for p in parts):
            raise ValueError("time must use HH:MM")
        hour, minute = map(int, parts)
        if not 0 <= hour <= 23 or not 0 <= minute <= 59:
            raise ValueError("time must use HH:MM")
        return f"{hour:02d}:{minute:02d}"


class HouseholdMemberIntent(BaseModel):
    id: str = Field(min_length=1, max_length=64, pattern=r"^[a-z0-9_-]+$")
    label: str = Field(min_length=1, max_length=80)
    legs: list[HouseholdLegIntent] = []


class ParsedQuery(BaseModel):
    price_min: int | None = None
    price_max: int | None = None
    rooms: list[int] | None = None            # [1,2] = «1-2 комнаты»
    area_min: float | None = None
    area_max: float | None = None
    geo: list[GeoConstraint] = []
    window_orientation: list[str] = []        # ["SW","W"]
    noise_max: Literal["low", "medium", "high"] | None = None
    stop_factors: list[str] = []              # ["bars","communal_flats"]
    area: str | None = None                   # район/сторона города: «север», «Сколково»
    semantic_text: str = ""                   # остаток для dense/sparse («двор-колодец»)
    lang: Literal["ru", "en"] = "ru"
    household: list[HouseholdMemberIntent] = []


class ResultItem(BaseModel):
    external_id: str
    price: int | None
    area: float | None
    rooms: int | None
    address_facts: dict          # walk_min_*, bar_density_500m, noise_level, orientation
    score: float                 # финальный score после реранка


class SearchResponse(BaseModel):
    results: list[ResultItem]
    explanation: str             # только поверх фактов из БД
    parsed: ParsedQuery          # что поняли (прозрачность/дебаг)
    relaxed: list[str] = []      # какие ограничения ослаблены relaxation-петлёй
    data_freshness: str          # «данные актуальны на …» (max updated_at)
    degraded: list[str] = []     # какие слои отвалились: "nlu"/"vector"/"reranker"/"llm"


class PointConstraint(BaseModel):
    """Кастомная гео-точка (компромисс «Сколково↔Сити»)."""
    lon: float = Field(ge=-180, le=180)
    lat: float = Field(ge=-90, le=90)
    minutes: int = Field(default=15, gt=0, le=60)
    mode: Literal["foot-walking", "cycling-regular", "driving-car"] = "foot-walking"


class SearchRequest(BaseModel):
    query: str = Field(min_length=1, max_length=2000)
    point: PointConstraint | None = None


class LineStringGeometry(BaseModel):
    type: Literal["LineString"] = "LineString"
    coordinates: list[LngLat]

    @field_validator("coordinates")
    @classmethod
    def validate_coordinates(cls, coordinates: list[tuple[float, float]]):
        if len(coordinates) < 2:
            raise ValueError("LineString requires at least two coordinates")
        return coordinates


class RouteLeg(BaseModel):
    to_label: str
    to_kind: DestinationKind
    mode: TravelMode
    depart: str
    arrive: str
    minutes: int = Field(ge=0)
    safety: LegSafety
    geometry: LineStringGeometry


class HouseholdMember(BaseModel):
    id: str
    label: str
    legs: list[RouteLeg]


class FamilyRoutingData(BaseModel):
    home: LngLat
    members: list[HouseholdMember]


class SocialScores(BaseModel):
    communal_share: float = Field(ge=0, le=1)
    bars_density: float = Field(ge=0, le=1)
    crime_index: float = Field(ge=0, le=1)


class SocialEnvironmentData(BaseModel):
    home: LngLat | None = None
    radius_m: int = Field(default=500, gt=0)
    scores: SocialScores
    heat: dict[str, Any]
    pois: list[dict[str, Any]] = []


class DirectLight(BaseModel):
    from_: str = Field(alias="from")
    to: str

    model_config = {"populate_by_name": True}


class SunHoursBySeason(BaseModel):
    winter: float = Field(ge=0)
    spring: float = Field(ge=0)
    summer: float = Field(ge=0)
    autumn: float = Field(ge=0)


class Obstruction(BaseModel):
    azimuth_deg: float = Field(ge=0, lt=360)
    elevation_deg: float = Field(ge=0, le=90)
    label: str


class ViewClimateData(BaseModel):
    orientation_deg: float = Field(ge=0, lt=360)
    direct_light: DirectLight
    sun_hours_by_season: SunHoursBySeason
    cloudiness_factor: float = Field(ge=0, le=1)
    obstructions: list[Obstruction]
    view_type: ViewType
    db: float = Field(ge=0)


class VerdictInfo(BaseModel):
    headline: str
    confidence: float = Field(ge=0, le=1)
    layers_checked: int = Field(ge=0)


class BriefItem(BaseModel):
    label: str
    status: BriefStatus


class CompromiseNote(BaseModel):
    block_key: str
    text: str


class RelaxationNote(BaseModel):
    text: str


class LifestyleBlock(BaseModel):
    key: str
    tier: BlockTier = "secondary"
    title: str
    icon: LifestyleIcon | None = None
    score: Grade
    verdict_line: str | None = None
    description: str
    metrics: dict[str, float | str] = {}
    data: FamilyRoutingData | SocialEnvironmentData | ViewClimateData | dict[str, Any] | None = None


class DossierPayload(BaseModel):
    verdict: VerdictInfo
    brief: list[BriefItem]
    blocks: list[LifestyleBlock]
    compromises: list[CompromiseNote] = []
    relaxation: list[RelaxationNote] = []
    zone_rationale: str = ""


class DossierRequest(BaseModel):
    object_id: str = Field(min_length=1, max_length=200)
    city: Literal["msk", "spb"] = "msk"
    raw_query: str = ""
    parsed_query: ParsedQuery = ParsedQuery()
    relaxed: list[str] = []
    degraded: list[str] = []


class DossierResponse(BaseModel):
    dossier: DossierPayload
    schema_version: Literal["dossier-v1"] = "dossier-v1"


class ObjectAskRequest(BaseModel):
    question: str = Field(min_length=1, max_length=2000)
    passport: dict[str, Any]
    search_context: dict[str, Any] = {}


class GroundedSentence(BaseModel):
    text: str = Field(min_length=1)
    evidence_paths: list[str] = []
    unknown: bool = False


class ObjectAskResponse(BaseModel):
    sentences: list[GroundedSentence]
