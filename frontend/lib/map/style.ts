// dataviz-light is a desaturated MapTiler style — reads as a neutral canvas so
// the periwinkle accent is the only saturated layer on the map.
export function mapStyleUrl(): string | null {
  const key = process.env.NEXT_PUBLIC_MAPTILER_KEY;
  if (!key) return null;
  return `https://api.maptiler.com/maps/dataviz-light/style.json?key=${key}`;
}

/**
 * Muted stage hues already used in the codebase — cool blue for point data,
 * warm amber for lines (noise/traffic), neutral slate for area fills. Kept
 * desaturated so the periwinkle accent stays the loudest thing on the map.
 * Shared by the map render layers AND the toggle legend swatches.
 */
export function layerPaintColor(geometryType: string): string {
  if (geometryType === "Point") return "#5AB8E0";
  if (geometryType === "LineString") return "#E0995A";
  return "#9BAAB8";
}
