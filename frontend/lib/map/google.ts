import { importLibrary, setOptions } from "@googlemaps/js-api-loader";

export type LngLat = [number, number];

export type GoogleMapsConfig = {
  apiKey: string;
  mapId: string;
};

export function googleMapsConfig(): GoogleMapsConfig | null {
  const apiKey = process.env.NEXT_PUBLIC_GOOGLE_MAPS_API_KEY?.trim();
  const mapId = process.env.NEXT_PUBLIC_GOOGLE_MAPS_MAP_ID?.trim();
  return apiKey && mapId ? { apiKey, mapId } : null;
}

export function toLatLng([lng, lat]: LngLat): google.maps.LatLngLiteral {
  return { lat, lng };
}

export function boundsLiteralToViewport(
  bounds: google.maps.LatLngBoundsLiteral,
): [number, number, number, number] {
  return [bounds.west, bounds.south, bounds.east, bounds.north];
}

export function collectGeoJsonPositions(
  data: GeoJSON.FeatureCollection | null | undefined,
): LngLat[] {
  const positions: LngLat[] = [];
  const collect = (node: unknown): void => {
    if (!Array.isArray(node)) return;
    if (typeof node[0] === "number" && typeof node[1] === "number") {
      positions.push([node[0], node[1]]);
      return;
    }
    node.forEach(collect);
  };
  const collectGeometry = (geometry: GeoJSON.Geometry | null): void => {
    if (!geometry) return;
    if (geometry.type === "GeometryCollection") {
      geometry.geometries.forEach(collectGeometry);
      return;
    }
    collect(geometry.coordinates);
  };
  for (const feature of data?.features ?? []) collectGeometry(feature.geometry);
  return positions;
}

let loadedForKey: string | null = null;
let librariesPromise: Promise<{
  maps: google.maps.MapsLibrary;
  marker: google.maps.MarkerLibrary;
}> | null = null;

/** Loads the browser API once. Public keys are protected by HTTP-referrer and
 * API restrictions in Google Cloud, not by hiding them from the browser. */
export function loadGoogleMaps(config: GoogleMapsConfig) {
  if (!librariesPromise) {
    loadedForKey = config.apiKey;
    setOptions({
      key: config.apiKey,
      v: "weekly",
      language: "ru",
      region: "RU",
      authReferrerPolicy: "origin",
      mapIds: [config.mapId],
    });
    librariesPromise = Promise.all([
      importLibrary("maps"),
      importLibrary("marker"),
    ]).then(([maps, marker]) => ({ maps, marker }));
  } else if (loadedForKey !== config.apiKey) {
    console.warn("Google Maps API is already loaded with another browser key");
  }
  return librariesPromise;
}

export function createLatLngBounds(positions: LngLat[]) {
  const bounds = new google.maps.LatLngBounds();
  positions.forEach((position) => bounds.extend(toLatLng(position)));
  return bounds;
}

export function clearDataLayer(layer: google.maps.Data) {
  layer.forEach((feature) => layer.remove(feature));
}

export function replaceDataLayer(
  layer: google.maps.Data,
  data: GeoJSON.FeatureCollection,
) {
  clearDataLayer(layer);
  layer.addGeoJson(data as GeoJSON.GeoJsonObject);
}

export function removeAdvancedMarker(
  marker: google.maps.marker.AdvancedMarkerElement | null | undefined,
) {
  if (marker) marker.map = null;
}

export function createProjectionOverlay(map: google.maps.Map) {
  const overlay = new google.maps.OverlayView();
  overlay.onAdd = () => undefined;
  overlay.draw = () => undefined;
  overlay.onRemove = () => undefined;
  overlay.setMap(map);
  return overlay;
}

export function projectLngLat(
  overlay: google.maps.OverlayView,
  position: LngLat,
): { x: number; y: number } | null {
  const projection = overlay.getProjection();
  if (!projection) return null;
  const point = projection.fromLatLngToDivPixel(new google.maps.LatLng(toLatLng(position)));
  return point ? { x: point.x, y: point.y } : null;
}
