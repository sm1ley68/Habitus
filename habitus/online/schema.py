# habitus/online/schema.py — единственный источник правды по формам данных online-фазы
from typing import Literal
from pydantic import BaseModel, Field


class GeoConstraint(BaseModel):
    kind: Literal["school", "metro", "park"]
    walk_minutes: int          # порог пешей доступности


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
    semantic_text: str = ""                   # остаток для dense/sparse («двор-колодец»)
    lang: Literal["ru", "en"] = "ru"


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
