import type { LayerId } from "@/lib/agent/types";
import { layerColor } from "./style";
import { poiIconImageId } from "./icons";

// Спецификации слоёв карты вынесены из MapCanvas: это чистые данные, которые
// можно проверить тестом, не поднимая maplibre и не имея ключа карты.
//
// Логика масштаба. Слои большие — 2351 школа, 1504 парка, 1432 бара, 276
// станций. Если рисовать значки и подписи на любом масштабе, включённые разом
// слои превращают карту в кашу. Поэтому на общем плане города остаются
// маленькие цветные точки (цвет различим и там), а значки и подписи
// проявляются при приближении к району.
export const ICON_FADE_START = 12.5;
export const ICON_FADE_END = 13.5;

type Expr = unknown;

const byZoom = (stops: Array<[number, number]>): Expr => [
  "interpolate", ["linear"], ["zoom"], ...stops.flat(),
];

/** Кружок точечного слоя: цвет слоя, радиус растёт с приближением. */
export function pointCirclePaint(id: LayerId): Record<string, Expr> {
  return {
    "circle-color": layerColor(id),
    "circle-radius": byZoom([[10, 3], [13, 5.5], [16, 9]]),
    "circle-opacity": 0,
    "circle-stroke-color": "#ffffff",
    "circle-stroke-width": byZoom([[10, 1], [14, 1.75]]),
    "circle-stroke-opacity": 0,
  };
}

export function iconLayerId(id: LayerId): string {
  return `layer-${id}-icon`;
}

/** Значок поверх кружка. Белый глиф уже нарисован в самой картинке. */
export function iconLayout(id: LayerId): Record<string, Expr> {
  return {
    "icon-image": poiIconImageId(id),
    // Размер согласован с радиусом кружка: глиф занимает ~60% диаметра.
    // Картинка 32px при pixelRatio 2 = 16 логических пикселей при size 1.
    "icon-size": byZoom([[13, 0.45], [16, 0.7]]),
    // Значок обязан оставаться на своей точке: прятать его при столкновении
    // нельзя, иначе слой выглядит дырявым. Подпись метро, наоборот, прячется.
    "icon-allow-overlap": true,
    "icon-ignore-placement": true,
  };
}

export function iconPaint(): Record<string, Expr> {
  return { "icon-opacity": byZoom([[ICON_FADE_START, 0], [ICON_FADE_END, 1]]) };
}

/**
 * Заливка скоплений баров. Прозрачность ведёт масштаб, и ведёт ОБРАТНО значкам:
 * это оценка «где сгущается ночная жизнь», осмысленная на плане города, но на
 * уровне улиц она бесполезна и вредна — буферы радиусом 500 м превращаются в
 * сплошное пятно, под которым не видно ни карты, ни точек. Проверка глазами
 * это подтвердила: на масштабе квартала фиолетовое накрывало всё.
 *
 * Поэтому: на общем плане — мягкий фон, при приближении гаснет, уступая место
 * отдельным точкам заведений, которые к тому моменту уже получили значки.
 */
export function densityFillOpacity(): Expr {
  return byZoom([[10, 0.18], [12.5, 0.12], [ICON_FADE_END, 0]]);
}

/**
 * Подпись станции метро. Текст требует шрифтов из стиля карты, поэтому
 * вызывающая сторона обязана проверить их наличие — см. hasGlyphs.
 */
export function metroLabelLayout(font: string[]): Record<string, Expr> {
  return {
    "text-field": ["get", "name"],
    "text-size": byZoom([[13, 11], [16, 13]]),
    "text-offset": [0, 1.4],
    "text-anchor": "top",
    "text-font": font,
    // Наложение подписей разрешать нельзя: 276 станций на общем плане слипнутся
    // в нечитаемую полосу. maplibre сам скроет те, что не помещаются.
    "text-allow-overlap": false,
    "text-optional": true,
  };
}

export function metroLabelPaint(): Record<string, Expr> {
  return {
    "text-color": "#3b3b45",
    "text-halo-color": "#ffffff",
    "text-halo-width": 1.5,
    "text-opacity": byZoom([[ICON_FADE_START, 0], [ICON_FADE_END, 1]]),
  };
}

export function metroLabelLayerId(): string {
  return "layer-metro-label";
}

/**
 * Есть ли в стиле карты шрифты для подписей. Стиль подключается извне и не
 * обязан их отдавать; без проверки maplibre ругается в консоль на каждый тайл,
 * а подписи всё равно не появляются. Нет шрифтов — нет слоя подписей, карта
 * при этом работает.
 */
export function hasGlyphs(style: { glyphs?: string } | null | undefined): boolean {
  return typeof style?.glyphs === "string" && style.glyphs.length > 0;
}

// Имя шрифта нельзя выдумывать: в наборе глифов стиля есть конкретные
// начертания, и запрос отсутствующего даёт пустые подписи. Берём то, которым
// подписан сам базовый стиль, — оно заведомо есть в наборе и заведомо покрывает
// кириллицу, раз им подписаны улицы Москвы.
export const FALLBACK_FONT = ["Noto Sans Regular"];

// Принимаем unknown и разбираем вручную: стиль приходит из чужой библиотеки,
// его тип описывает десятки видов слоёв, и подгонять их под свою форму
// пришлось бы приведением, которое всё равно ничего не проверяет.
export function pickLabelFont(style: unknown): string[] {
  const layers = (style as { layers?: unknown })?.layers;
  if (!Array.isArray(layers)) return FALLBACK_FONT;
  for (const layer of layers) {
    if ((layer as { type?: unknown })?.type !== "symbol") continue;
    const font = (layer as { layout?: Record<string, unknown> })?.layout?.["text-font"];
    if (Array.isArray(font) && font.length > 0 && font.every((f) => typeof f === "string")) {
      return font as string[];
    }
  }
  return FALLBACK_FONT;
}
