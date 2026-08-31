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


# Намерение реплики в многоходовом чате: новый поиск / правка прошлого разбора /
# вопрос про уже показанную выдачу без нового поиска.
TurnIntent = Literal["new_search", "refine", "followup"]
MetroSystem = Literal["subway", "mck", "mcd"]


class ParsedTurn(BaseModel):
    """Разбор одной реплики чата с учётом (или без) предыдущего ParsedQuery."""
    intent: TurnIntent = "new_search"
    query: ParsedQuery = ParsedQuery()
    cleared_fields: list[str] = []   # какие ограничения пользователь снял

    @field_validator("cleared_fields")
    @classmethod
    def drop_unknown_field_names(cls, value: list[str]) -> list[str]:
        # LLM иногда путает имена полей — молча отбрасываем то, чего нет в
        # ParsedQuery, вместо того чтобы ронять весь разбор реплики.
        return [name for name in value if name in ParsedQuery.model_fields]


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
    # честные примечания о низком покрытии поля, учтённого как мягкий сигнал, а
    # не фильтр (например, window_orientation); реально посчитаны по БД,
    # выдуманных процентов не бывает — не удалось посчитать, заметки нет
    notes: list[str] = []
    data_freshness: str          # «данные актуальны на …» (max updated_at)
    degraded: list[str] = []     # какие слои отвалились: "nlu"/"vector"/"reranker"/"llm"
    intent: TurnIntent = "new_search"   # намерение реплики (multi-turn чат)
    area_label: str | None = None    # человекочитаемая зона: «центр (ЦАО)», «Хамовники»
    area_geojson: dict | None = None  # FeatureCollection границы зоны для карты
    # мс по стадиям (parse/encode/resolve_area/retrieval/rerank/explain), из
    # trace.collector(); стадия, которая не выполнилась в этом запросе, в
    # словарь не попадает — нулей вместо отсутствующего замера не выдумываем
    timings: dict[str, float] = {}
    # диагностика ограничений: сколько объектов остаётся при последовательном
    # наложении клауз build_where (retrieval.constraint_diagnostics). Считается
    # только когда results пуст — иначе непонятно, какое условие обнулило
    # выборку, а на непустой выдаче лишние COUNT'ы не нужны.
    diagnostics: list[dict] = []


class PointConstraint(BaseModel):
    """Кастомная гео-точка (компромисс «Сколково↔Сити»)."""
    lon: float = Field(ge=-180, le=180)
    lat: float = Field(ge=-90, le=90)
    minutes: int = Field(default=15, gt=0, le=60)
    # "metro" считается внутренним движком по графу, остальные — изохронами ORS
    mode: Literal["foot-walking", "cycling-regular", "driving-car", "metro"] = "foot-walking"


class SearchRequest(BaseModel):
    query: str = Field(min_length=1, max_length=2000)
    point: PointConstraint | None = None
    city: Literal["msk", "spb"] = "msk"
    # False — объяснение забирается отдельно через /explain/stream (так делает
    # шлюз). Умолчание True сохраняет прежний однократный ответ для CLI и eval.
    explain: bool = True
    # Разбор предыдущего шага диалога (из chat_searches на стороне шлюза).
    # None — независимый запрос, поведение как раньше.
    prev_parsed: ParsedQuery | None = None
    # Сколько объектов вернуть сверх первой страницы (запас для «показать ещё»
    # на стороне шлюза). None — дефолт settings.result_max_n.
    top_n: int | None = Field(default=None, gt=0, le=50)


class ExplainRequest(BaseModel):
    """Вход /explain/stream: тот же запрос и та же выдача, что вернул /search."""
    query: str = Field(min_length=1, max_length=2000)
    results: list[ResultItem] = []
    relaxed: list[str] = []
    # честные примечания из того же /search (например реальное покрытие данных
    # об ориентации окон) — без них потоковое объяснение не знает о них вовсе
    notes: list[str] = []


class LineStringGeometry(BaseModel):
    type: Literal["LineString"] = "LineString"
    coordinates: list[LngLat]

    @field_validator("coordinates")
    @classmethod
    def validate_coordinates(cls, coordinates: list[tuple[float, float]]):
        if len(coordinates) < 2:
            raise ValueError("LineString requires at least two coordinates")
        return coordinates


class MetroSegment(BaseModel):
    """Отрезок поездки по одной линии без пересадок."""
    line_ref: str
    line_name: str
    system: MetroSystem
    colour: str | None = None
    from_station: str
    to_station: str
    stops: int = Field(ge=1)
    minutes: int = Field(ge=0)
    # true — время выведено из расстояния, а не взято из курируемого файла.
    # Признак едет до фронта: оценка показывается как оценка.
    estimated: bool = False


class MetroTransfer(BaseModel):
    from_station: str
    to_station: str
    minutes: int = Field(ge=0)
    # Переход улицей (типично между метро и МЦД) — рисуется отдельным пешим
    # сегментом, а не сливается в общий «переход»: он вдвое-втрое длиннее.
    outdoor: bool = False
    estimated: bool = False


class MetroRide(BaseModel):
    """Разбивка метро-ноги. Итог «от двери до двери» живёт в RouteLeg.minutes,
    здесь — из чего он сложился. Реальный инвариант: walk_from_home_min +
    сумма minutes всех segments + сумма minutes всех transfers +
    walk_to_dest_min + wait_min == RouteLeg.minutes (total_minutes). Фронт
    показывает разбивку и не складывает её заново, иначе округления
    разойдутся."""
    walk_from_home_min: int = Field(ge=0)
    walk_to_dest_min: int = Field(ge=0)
    segments: list[MetroSegment] = []
    transfers: list[MetroTransfer] = []
    total_minutes: int = Field(ge=0)
    # R69b (фикс-раунд 2): это НЕ независимый замер ожидания посадки, а
    # остаток округления — total_minutes минус уже показанные части (оба
    # пеших плеча, все segments, все transfers), каждая из которых округлена
    # НЕЗАВИСИМО. Без этого поля инвариант «сумма частей == total_minutes»
    # ломался бы на каждой поездке: ожидание реально заложено в
    # total_minutes графом (Задача 9), но не несётся ни одним
    # MetroSegment/MetroTransfer по отдельности. Остаток гарантирует
    # инвариант ПО ПОСТРОЕНИЮ, а не совпадением округлений — платой служит
    # то, что значение может отличаться от «настоящего» интервала на пару
    # минут (вбирает чужие округления), особенно на маршрутах с несколькими
    # пересадками. Обязательное поле, без дефолта: 0 неотличим от
    # «ожидания нет», а для реального рельсового маршрута это всегда ложь
    # (headway линии посева всегда > 0, R29/R30).
    wait_min: int = Field(ge=0)
    estimated: bool = False


class RouteLeg(BaseModel):
    to_label: str
    to_kind: DestinationKind
    mode: TravelMode
    depart: str
    arrive: str
    minutes: int = Field(ge=0)
    safety: LegSafety
    geometry: LineStringGeometry
    # Разбивка поездки на рельсовом транспорте. None у ног любого другого
    # режима — существующие потребители RouteLeg не ломаются.
    metro: MetroRide | None = None


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


# --- Объявление из личного кабинета продавца -------------------------------
# Витрина принимает его той же формой, что и объявление источника: разница
# только в source и в том, что строка помечается owner_managed.

class OwnerListingUpsertRequest(BaseModel):
    external_id: str = Field(min_length=1, max_length=128)
    source: Literal["cian", "owner"]
    city: Literal["msk", "spb"]
    price: int | None = None
    area: float | None = None
    kitchen_area: float | None = None
    rooms: int | None = None
    level: int | None = None
    levels: int | None = None
    address: str = ""
    lng: float
    lat: float
    window_orientation: list[str] = Field(default_factory=list)
    description: str = ""
    photos: list[str] = Field(default_factory=list)
    source_url: str = ""


class OwnerListingUpsertResponse(BaseModel):
    external_id: str
    indexed: bool


class OwnerListingWithdrawRequest(BaseModel):
    external_id: str = Field(min_length=1, max_length=128)


class OwnerListingWithdrawResponse(BaseModel):
    external_id: str
    deactivated: bool
