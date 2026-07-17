// Образцы данных ТОЛЬКО для тестов. Продакшн-код сюда не импортируется и не
// должен: приложение берёт все факты из бэка (lib/api/*). Формы здесь повторяют
// контракт, чтобы тесты рендерили компоненты на реалистичном входе.
import type { Property, LifestyleBlock, HistoryItem, GeoZone, LayerId, Dossier } from "@/lib/agent/types";
import { LAYER_LABELS } from "@/lib/agent/types";

// Real SPb coordinates near Лицей 239 (Кирочная ул.).
export const SCHOOL_239: [number, number] = [30.3479, 59.9439];

// The resident's front door — origin of every logistics route. [lng, lat].
export const LOGISTICS_HOME: [number, number] = [30.3625, 59.949];

export const PROPERTIES: Property[] = [
  { id: "jk-neva-residence", name: "ЖК Neva Residence", cover_image: "/covers/neva.svg", match_score: 96, price_from: 18500000, rooms: 3, area_sqm: 78.5, floor: "7/12", tags: ["11 минут до школы", "0% баров вокруг", "Окна во двор-парк"], coordinates: [30.3625, 59.9490] },
  { id: "jk-ligovsky-garden", name: "ЖК Ligovsky Garden", cover_image: "/covers/ligovsky.svg", match_score: 91, price_from: 15200000, rooms: 2, area_sqm: 54, floor: "4/9", tags: ["8 минут до сада", "Тихий двор", "Рядом парк Есенина"], coordinates: [30.3550, 59.9280] },
  { id: "jk-zelenogorsk-view", name: "ЖК Zelenogorsk View", cover_image: "/covers/zelenogorsk.svg", match_score: 87, price_from: 21000000, rooms: 3, area_sqm: 82, floor: "12/16", tags: ["Вид на залив", "15 минут до школы №47", "Низкий шумовой фон"], coordinates: [30.3400, 59.9520] },
  { id: "jk-rechnoy-kvartal", name: "ЖК Rechnoy Kvartal", cover_image: "/covers/rechnoy.svg", match_score: 78, price_from: 12800000, rooms: 2, area_sqm: 48, floor: "2/5", tags: ["5 минут до метро", "Рядом ТЦ", "Шум требует уточнения"], coordinates: [30.3700, 59.9410] },
];

// Search zone polygon loosely enclosing the properties (ring closed).
export const ZONE_GEOJSON: GeoZone = {
  type: "FeatureCollection",
  features: [{
    type: "Feature",
    properties: { area_type: "recommended_zone" },
    geometry: {
      type: "Polygon",
      coordinates: [[
        [30.335, 59.955], [30.375, 59.953], [30.378, 59.930],
        [30.350, 59.922], [30.332, 59.938], [30.335, 59.955],
      ]],
    },
  }],
};

function fc(features: GeoJSON.Feature[]): GeoJSON.FeatureCollection {
  return { type: "FeatureCollection", features };
}
const pt = (coordinates: [number, number], properties: Record<string, unknown> = {}): GeoJSON.Feature =>
  ({ type: "Feature", properties, geometry: { type: "Point", coordinates } });

export const LAYER_GEOJSON: Record<LayerId, GeoJSON.FeatureCollection> = {
  schools: fc([pt(SCHOOL_239, { name: "ФМЛ №239", rating: "Top-1" }), pt([30.358, 59.945], { name: "Школа №47" })]),
  parks: fc([pt([30.352, 59.948], { name: "Сквер" }), pt([30.366, 59.935], { name: "Парк Есенина" })]),
  bars: fc([pt([30.361, 59.941], { name: "Бар" }), pt([30.369, 59.944], { name: "Алкомаркет" })]),
  ecology: fc([pt([30.345, 59.951], { weight: 0.8 })]),
  communal: fc([{ type: "Feature", properties: { weight: 0.9 }, geometry: { type: "Polygon", coordinates: [[[30.370, 59.940], [30.377, 59.940], [30.377, 59.946], [30.370, 59.946], [30.370, 59.940]]] } }]),
  noise: fc([{ type: "Feature", properties: { db_level: 75, source: "Магистраль" }, geometry: { type: "LineString", coordinates: [[30.332, 59.950], [30.378, 59.948]] } }]),
};

export const LIFESTYLE_BLOCKS: LifestyleBlock[] = [
  {
    key: "family_routing",
    tier: "hero",
    title: "Суточный ритм семьи",
    icon: "route",
    score: "A",
    verdict_line: "Все доедут вовремя, сын идёт в школу сам и без опасных переходов.",
    description: "Сын доходит до Лицея 239 за 11 минут без крупных проспектов. Мама успевает на работу через Чернышевскую, папа выезжает на машине против потока.",
    metrics: { childWalkMin: 11, momCommuteMin: 22, dadDriveMin: 17 },
    data: {
      home: LOGISTICS_HOME,
      members: [
        {
          id: "child",
          label: "Сын",
          legs: [
            {
              to_label: "Лицей 239",
              to_kind: "school",
              mode: "walk",
              depart: "08:15",
              arrive: "08:26",
              minutes: 11,
              safety: "safe",
              geometry: {
                type: "LineString",
                coordinates: [LOGISTICS_HOME, [30.3571, 59.9472], [30.3512, 59.9451], SCHOOL_239],
              },
            },
          ],
        },
        {
          id: "mom",
          label: "Мама",
          legs: [
            {
              to_label: "м. Чернышевская",
              to_kind: "metro",
              mode: "walk",
              depart: "08:30",
              arrive: "08:36",
              minutes: 6,
              safety: "safe",
              geometry: {
                type: "LineString",
                coordinates: [LOGISTICS_HOME, [30.3598, 59.9451], [30.356, 59.941]],
              },
            },
            {
              to_label: "Работа (Сенная)",
              to_kind: "work",
              mode: "metro",
              depart: "08:38",
              arrive: "08:52",
              minutes: 14,
              safety: "safe",
              geometry: {
                type: "LineString",
                coordinates: [[30.356, 59.941], [30.347, 59.937], [30.339, 59.933]],
              },
            },
          ],
        },
        {
          id: "dad",
          label: "Папа",
          legs: [
            {
              to_label: "Бизнес-центр «Санкт-Петербург Плаза»",
              to_kind: "work",
              mode: "car",
              depart: "09:00",
              arrive: "09:17",
              minutes: 17,
              safety: "caution",
              geometry: {
                type: "LineString",
                coordinates: [LOGISTICS_HOME, [30.3691, 59.9463], [30.3742, 59.9401], [30.371, 59.933]],
              },
            },
          ],
        },
      ],
    },
  },
  {
    key: "social_environment",
    tier: "hero",
    title: "Соцслой и скрытый контингент",
    icon: "users",
    score: "A-",
    verdict_line: "Ни одной коммуналки под окнами, бары — за периметром двора.",
    description: "В радиусе 500 м нерасселённого фонда нет, единственный бар работает в квартале к востоку. Крим-индекс ниже среднего по Центральному району.",
    metrics: { radiusM: 500, communalShare: 0.05, barsWithin500m: 1 },
    data: {
      radius_m: 500,
      home: LOGISTICS_HOME,
      scores: { communal_share: 0.05, bars_density: 0.12, crime_index: 0.18 },
      heat: {
        type: "FeatureCollection",
        features: [
          {
            type: "Feature",
            properties: { layer: "communal", weight: 0.9 },
            geometry: LAYER_GEOJSON.communal.features[0].geometry,
          },
          {
            type: "Feature",
            properties: { layer: "bars", weight: 0.6 },
            geometry: LAYER_GEOJSON.bars.features[0].geometry,
          },
          {
            type: "Feature",
            properties: { layer: "bars", weight: 0.35 },
            geometry: LAYER_GEOJSON.bars.features[1].geometry,
          },
          {
            type: "Feature",
            properties: { layer: "crime", weight: 0.45 },
            geometry: { type: "Point", coordinates: [30.3668, 59.9438] },
          },
        ],
      },
      pois: [
        { kind: "bar", coordinates: [30.361, 59.941], label: "Бар (24ч)" },
        { kind: "communal", coordinates: [30.3735, 59.943], label: "Нерасселённый корпус" },
      ],
    },
  },
  {
    key: "view_and_climate",
    tier: "hero",
    title: "Взгляд из окна и свет",
    icon: "sun",
    score: "B+",
    verdict_line: "Двор-парк без баров, но утром — тень соседнего корпуса.",
    description: "Окна выходят на юго-запад во двор-парк. Прямое солнце с 14:00 до 18:00, до полудня двор в тени соседнего корпуса. Уровень шума минимальный.",
    metrics: { orientationDeg: 225, directLightFrom: 14, directLightTo: 18, db: 35 },
    data: {
      orientation_deg: 225,
      direct_light: { from: "14:00", to: "18:00" },
      sun_hours_by_season: { winter: 1.5, spring: 4, summer: 6, autumn: 3 },
      cloudiness_factor: 0.6,
      obstructions: [
        { azimuth_deg: 120, elevation_deg: 22, label: "Соседний корпус" },
      ],
      view_type: "courtyard_park",
      db: 35,
    },
  },
  { key: "ecology", tier: "secondary", title: "Экология", icon: "leaf", score: "A-", description: "Рядом два сквера, ближайшая промзона — в 2 км.", metrics: { greenSpaces: 2, industrialKm: 2 } },
  { key: "quiet", tier: "secondary", title: "Тишина", icon: "volume", score: "A-", description: "Окна во двор-парк, шумовой фон ниже среднего по району.", metrics: { db: 35 } },
];

export const DOSSIER: Dossier = {
  verdict: {
    headline: "Идеально по школе и безопасности — компромисс по утреннему свету",
    confidence: 0.9,
    layers_checked: 6,
  },
  brief: [
    { label: "Лицей 239 ≤ 15 мин пешком", status: "met" },
    { label: "Без коммуналок в радиусе 500 м", status: "met" },
    { label: "Ни одного бара под окнами", status: "met" },
    { label: "Прямое солнце в первой половине дня", status: "compromise" },
    { label: "Тишина под окнами", status: "met" },
  ],
  compromises: [
    {
      block_key: "view_and_climate",
      text: "Утром двор в тени соседнего корпуса, прямое солнце приходит только после 14:00.",
    },
  ],
  relaxation: [
    {
      text: "В радиусе 15 минут без крупных проспектов идеальных вариантов не нашлось — расширил окно поиска до 18 минут пешком.",
    },
  ],
  zone_rationale:
    "Пересечение 15-минутной доступности Лицея 239 и языкового центра на Чернышевской, минус кварталы с нерасселённым фондом вдоль Суворовского.",
};

export const HISTORY: HistoryItem[] = [
  { title: "Квартира для семьи с ребёнком, тихий двор", time: "Сегодня, 8:40" },
  { title: "Удалёнка + вид без баров снизу", time: "Вчера" },
  { title: "Компромисс между двумя работами", time: "3 дня назад" },
  { title: "Рядом с парком, минимум шума", time: "На прошлой неделе" },
  { title: "До сильной школы за 15 минут", time: "2 недели назад" },
];

export const MAP_LAYER_IDS = Object.keys(LAYER_LABELS) as LayerId[];
export { LAYER_LABELS };

export const ANSWER_TEXT =
  "Нашёл 4 варианта под ваш сценарий. Лучше всего подходит ЖК Neva Residence: до сильной школы 11 минут пешком без опасных переходов, окна во двор-парк, рядом нет баров. Ниже — карта зоны и карточки объектов, отсортированные по совпадению с запросом.";
