export type Stage =
  | "idle" | "linguistic" | "geo" | "context"
  | "relaxation" | "streaming" | "done" | "error";

export type AgentName = "linguistic" | "geo" | "context" | "orchestrator";
export type AgentEventStatus = "processing" | "done" | "relaxation_triggered";

export interface AgentEvent {
  agent: AgentName;
  status: AgentEventStatus;
  message: string;
  token?: string; // present during streaming
}

export type LifestyleIcon =
  | "school" | "users" | "sun" | "volume" | "leaf" | "hospital" | "route";

export type LayerId =
  | "communal" | "noise" | "schools" | "bars" | "crime" | "parks" | "metro";

// Слои, у которых есть СВОЙ тумблер. `crime` сюда не входит намеренно: это
// буферы вокруг тех же точек bar/alcohol (4466 фич на 4479 точек), то есть не
// второй слой, а второе прочтение первого. Два контрола на одну сущность —
// путаница, поэтому скопления включаются вместе с «Барами».
//
// Сам ключ `crime` остаётся: он зафиксирован на трёх сторонах (CHECK в
// schema.sql ↔ Go AllowedLayers ↔ LayerId). И «риск-зонами» он не называется
// нигде — статистики преступности за ним нет, только плотность алкоточек.
export type ToggleLayerId = Exclude<LayerId, "crime">;

export const LAYER_LABELS: Record<ToggleLayerId, string> = {
  communal: "Коммунальный фонд",
  noise: "Шум",
  schools: "Школы",
  bars: "Бары",
  parks: "Парки",
  metro: "Метро",
};

export const MAP_LAYER_IDS = Object.keys(LAYER_LABELS) as ToggleLayerId[];

// Порядок отрисовки на карте: скопления идут ПЕРВЫМИ, чтобы мягкая заливка
// легла под точки заведений, а не накрыла их.
export const RENDERED_LAYER_IDS: LayerId[] = ["crime", ...MAP_LAYER_IDS];

export interface GeoZone {
  type: "FeatureCollection";
  features: Array<{
    type: "Feature";
    properties: Record<string, unknown>;
    geometry: {
      type: "Polygon" | "MultiPolygon";
      coordinates: number[][][] | number[][][][];
    };
  }>;
}

export interface Property {
  id: string;
  name: string;
  address: string;
  cover_image: string;
  match_score: number;
  price_from: number | null;
  rooms: number | null;
  area_sqm: number | null;
  floor: string;
  tags: string[];
  coordinates: [number, number]; // [lng, lat]
}

/** Диагностика пустой выдачи: сколько объектов оставалось после каждой клаузы
 *  фильтра, в порядке их наложения (habitus/online/retrieval.py). Приходит
 *  только когда результатов ноль. */
export interface ConstraintDiagnostic {
  constraint: string;
  remaining: number;
}

export type WaypointKind =
  | "home" | "park" | "crossing" | "school" | "bar" | "metro" | "poi";

export interface Waypoint {
  label: string;
  kind: WaypointKind;
}

// A real place the resident routinely walks to. Kinds get distinct muted map
// pins; coordinates are [lng, lat] like everything else in the geo layer.
export type DestinationKind = "school" | "metro" | "work" | "park";

export interface Destination {
  label: string;
  kind: DestinationKind;
  coordinates: [number, number]; // [lng, lat]
}

// --- Dossier top-level (Н.1) ---
export type BriefStatus = "met" | "compromise" | "relaxed" | "unknown";
export interface BriefItem { label: string; status: BriefStatus; }
export interface VerdictInfo { headline: string; confidence: number; layers_checked: number; }
export interface CompromiseNote { block_key: string; text: string; }
export interface RelaxationNote { text: string; }

export interface Dossier {
  verdict: VerdictInfo;
  brief: BriefItem[];
  compromises: CompromiseNote[];
  relaxation: RelaxationNote[];
  zone_rationale: string;
}

export type BlockTier = "hero" | "secondary";

// --- Hero data payloads (Н.3) ---
export type TravelMode = "walk" | "scooter" | "bus" | "car" | "metro";
export type LegSafety = "safe" | "caution";

/** Зафиксировано на трёх сторонах: habitus/online/schema.py ↔ Go ↔ здесь. */
export type MetroSystem = "subway" | "mck" | "mcd";

export interface MetroSegment {
  line_ref: string;
  line_name: string;
  system: MetroSystem;
  // Из OSM приходит CSS-имя цвета («red»), а не #rrggbb — hex не предполагать.
  colour: string | null;
  from_station: string;
  to_station: string;
  stops: number;
  minutes: number;
  /** Время выведено из расстояния, а не взято из курируемого файла. */
  estimated: boolean;
}

export interface MetroTransfer {
  from_station: string;
  to_station: string;
  minutes: number;
  /** Переход улицей (типично метро↔МЦД) — рисуется отдельным пешим шагом. */
  outdoor: boolean;
  estimated: boolean;
}

/** Разбивка метро-ноги. Итог «от двери до двери» — в RouteLeg.minutes;
 *  здесь то, из чего он сложился. Складывать разбивку заново нельзя:
 *  округления разойдутся с итогом. */
export interface MetroRide {
  walk_from_home_min: number;
  walk_to_dest_min: number;
  segments: MetroSegment[];
  transfers: MetroTransfer[];
  total_minutes: number;
  /** Не независимый замер ожидания посадки, а остаток округления:
   *  total_minutes минус уже показанные части (оба пеших плеча, все
   *  segments, все transfers), каждая из которых округлена независимо.
   *  Обязательное поле, без дефолта: 0 неотличим от «ожидания нет», а для
   *  реального рельсового маршрута это всегда ложь (headway линии всегда
   *  > 0). Инвариант: walk_from_home_min + Σ segments.minutes +
   *  Σ transfers.minutes + walk_to_dest_min + wait_min === total_minutes. */
  wait_min: number;
  estimated: boolean;
}

export interface RouteLeg {
  to_label: string;
  to_kind: DestinationKind | "poi";
  mode: TravelMode;
  depart: string;   // "08:15"
  arrive: string;   // "08:26"
  minutes: number;
  safety: LegSafety;
  geometry: { type: "LineString"; coordinates: [number, number][] };
  // Разбивка поездки на рельсовом транспорте. Отсутствует у ног любого
  // другого режима — существующие потребители RouteLeg не ломаются.
  metro?: MetroRide;
}
export interface HouseholdMember { id: string; label: string; legs: RouteLeg[]; }
export interface FamilyRoutingData { home: [number, number]; members: HouseholdMember[]; }

export type SocialLayerId = "communal" | "bars" | "crime";
export interface SocialScores { communal_share: number; bars_density: number; crime_index: number; }
export interface SocialEnvironmentData {
  radius_m: number;
  scores: SocialScores;
  heat: GeoJSON.FeatureCollection; // features[].properties.layer ∈ SocialLayerId, .weight 0..1
  pois?: Array<{ kind: string; coordinates: [number, number]; label: string }>;
  home?: [number, number]; // [lng, lat] map centre; falls back to a constant when absent
}

export type Season = "winter" | "spring" | "summer" | "autumn";
export type ViewType = "courtyard_park" | "street" | "water" | "wall" | "well";
export interface Obstruction { azimuth_deg: number; elevation_deg: number; label: string; }
export interface ViewClimateData {
  orientation_deg: number;
  direct_light: { from: string; to: string };
  sun_hours_by_season: Record<Season, number>;
  cloudiness_factor: number; // 0..1
  obstructions: Obstruction[];
  view_type: ViewType;
  db: number;
}

export type HeroBlockData = FamilyRoutingData | SocialEnvironmentData | ViewClimateData;

export interface LifestyleBlock {
  key: string;
  title: string;
  icon: LifestyleIcon;
  score: string; // "A+", "A-", "B+", "B", "C", "D"
  description: string;
  metrics?: Record<string, number | string>;
  waypoints?: Waypoint[];
  destinations?: Destination[];
  tier?: BlockTier;         // NEW — default "secondary" when absent
  verdict_line?: string;    // NEW
  data?: HeroBlockData;     // NEW
}

export interface HistoryItem { title: string; time: string; }
export type City = "spb" | "msk";

// --- Composed API response (contract §4 + Н.1–Н.3) ---
// The exact shape returned by GET /objects/{id}. Field names/types mirror the
// backend so the passport screen consumes it with zero mapping.
export interface LifestyleAnalysis {
  match_score: number;
  summary: string;
  verdict: VerdictInfo;
  brief: BriefItem[];
  blocks: LifestyleBlock[];
  compromises: CompromiseNote[];
  relaxation: RelaxationNote[];
  zone_rationale: string;
}
export interface ObjectPassport {
  id: string; name: string; address: string; price: number;
  rooms: number; area_sqm: number; floor: string;
  images: string[]; coordinates: [number, number];
  lifestyle_analysis: LifestyleAnalysis;
}
