import type { Property } from "@/lib/agent/types";

export type Viewport = [number, number, number, number];

export interface RankedProperty {
  property: Property;
  sourceIndex: number;
  distanceToCenter: number;
}

export function isValidLngLat(value: unknown): value is [number, number] {
  if (!Array.isArray(value) || value.length < 2) return false;
  const [lng, lat] = value;
  return Number.isFinite(lng) && Number.isFinite(lat)
    && lng >= -180 && lng <= 180 && lat >= -90 && lat <= 90;
}

function longitudeInViewport(lng: number, west: number, east: number) {
  return west <= east ? lng >= west && lng <= east : lng >= west || lng <= east;
}

export function isInViewport(
  coordinates: [number, number],
  [west, south, east, north]: Viewport,
) {
  if (!isValidLngLat(coordinates)) return false;
  const [lng, lat] = coordinates;
  return longitudeInViewport(lng, west, east) && lat >= south && lat <= north;
}

export function viewportCenter([west, south, east, north]: Viewport): [number, number] {
  const unwrappedEast = east < west ? east + 360 : east;
  const rawLng = (west + unwrappedEast) / 2;
  const lng = rawLng > 180 ? rawLng - 360 : rawLng;
  return [lng, (south + north) / 2];
}

export function distanceToViewportCenter(
  coordinates: [number, number],
  viewport: Viewport,
) {
  const [centerLng, centerLat] = viewportCenter(viewport);
  const [lng, lat] = coordinates;
  const lngDelta = Math.min(
    Math.abs(lng - centerLng),
    360 - Math.abs(lng - centerLng),
  ) * Math.cos(centerLat * Math.PI / 180);
  const latDelta = lat - centerLat;
  return lngDelta * lngDelta + latDelta * latDelta;
}

export function expandViewport(
  [west, south, east, north]: Viewport,
  ratio = 0.1,
): Viewport {
  const unwrappedEast = east < west ? east + 360 : east;
  const lngPadding = (unwrappedEast - west) * ratio;
  const latPadding = (north - south) * ratio;
  let expandedWest = west - lngPadding;
  let expandedEast = unwrappedEast + lngPadding;
  if (expandedWest < -180) expandedWest += 360;
  if (expandedEast > 180) expandedEast -= 360;
  return [
    expandedWest,
    Math.max(-90, south - latPadding),
    expandedEast,
    Math.min(90, north + latPadding),
  ];
}

/** Сдвинулся ли вьюпорт настолько, чтобы перезапрашивать данные.
 *
 *  Любой `idle` карты раньше публиковал новый вьюпорт, а тот запускал полный
 *  цикл «скачать объявления и все активные слои» — мегабайты на каждое
 *  микродвижение. Порог измеряется в долях ТЕКУЩЕГО вьюпорта, поэтому работает
 *  одинаково на любом зуме. */
export function viewportChangedEnough(
  previous: Viewport | null,
  next: Viewport,
  ratio = 0.02,
): boolean {
  if (!previous) return true;
  const [west, south, east, north] = previous;
  const width = Math.abs((east < west ? east + 360 : east) - west);
  const height = Math.abs(north - south);
  const lngTolerance = width * ratio;
  const latTolerance = height * ratio;
  return Math.abs(next[0] - west) > lngTolerance
    || Math.abs(next[2] - east) > lngTolerance
    || Math.abs(next[1] - south) > latTolerance
    || Math.abs(next[3] - north) > latTolerance;
}

/** Match stays primary. Scores inside one five-point band are close enough
 * for proximity to the map centre to become the tie-breaker. */
export function rankVisibleProperties(
  properties: Property[],
  viewport: Viewport | null,
): RankedProperty[] {
  const ranked = properties
    .map((property, sourceIndex) => ({
      property,
      sourceIndex,
      distanceToCenter: viewport
        ? distanceToViewportCenter(property.coordinates, viewport)
        : 0,
    }))
    .filter(({ property }) => isValidLngLat(property.coordinates))
    .filter(({ property }) => !viewport || isInViewport(property.coordinates, viewport));

  return ranked.sort((a, b) => {
    const aBand = Math.floor(a.property.match_score / 5);
    const bBand = Math.floor(b.property.match_score / 5);
    return bBand - aBand
      || a.distanceToCenter - b.distanceToCenter
      || b.property.match_score - a.property.match_score
      || a.sourceIndex - b.sourceIndex;
  });
}

