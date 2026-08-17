import type { LayerId } from "@/lib/agent/types";
// dataviz-light is a desaturated MapTiler style — reads as a neutral canvas so
// the periwinkle accent is the only saturated layer on the map.
export function mapStyleUrl(): string | null {
  const key = process.env.NEXT_PUBLIC_MAPTILER_KEY;
  if (!key) return null;
  return `https://api.maptiler.com/maps/dataviz-light/style.json?key=${key}`;
}

/**
 * Цвет СЛОЯ, а не типа геометрии. Раньше цвет выбирался по геометрии, и все
 * точечные слои — школы, парки, бары, метро — красились одним синим: на карте
 * они были неразличимы, а легенда честно показывала четыре одинаковых кружка.
 *
 * Оттенки разведены по кругу и выбраны узнаваемо: зелень для парков, красный
 * московского метро, тёплое золото для школ, фиолетовый ночной жизни для
 * баров. Синий освобождён намеренно — рядом живёт барвинковый акцент
 * (--accent: #6f7cc8), которым помечены НАШИ объекты, и путать их с городскими
 * точками нельзя.
 */
export const LAYER_COLORS: Record<LayerId, string> = {
  parks: "#3E8E63",
  schools: "#C98A1E",
  bars: "#7B4FBF",
  metro: "#D64545",
  // Скопления баров — тот же слой, что и бары, поэтому тот же цвет: иначе
  // заливка читается как отдельная непонятная сущность.
  crime: "#7B4FBF",
  // Линии шума и полигоны фонда остаются прежними: они не точки и ни с чем
  // не сливались.
  noise: "#E0995A",
  communal: "#9BAAB8",
};

export function layerColor(id: LayerId): string {
  return LAYER_COLORS[id];
}

/** Фолбэк по геометрии — для данных, пришедших вне известного слоя. */
export function layerPaintColor(geometryType: string): string {
  if (geometryType === "Point") return "#5AB8E0";
  if (geometryType === "LineString") return "#E0995A";
  return "#9BAAB8";
}
