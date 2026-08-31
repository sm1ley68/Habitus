"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AnimatePresence } from "framer-motion";
import { MarkerClusterer } from "@googlemaps/markerclusterer";
import { useGoogleMap } from "@/lib/map/useGoogleMap";
import {
  boundsLiteralToViewport,
  collectGeoJsonPositions,
  createLatLngBounds,
  createProjectionOverlay,
  projectLngLat,
  removeAdvancedMarker,
  replaceDataLayer,
  toLatLng,
} from "@/lib/map/google";
import { layerColor, layerPaintColor, metroLineColor } from "@/lib/map/style";
import {
  isInViewport,
  isValidLngLat,
  rankVisibleProperties,
} from "@/lib/map/viewport";
import { useSession } from "@/lib/store/session";
import { RENDERED_LAYER_IDS, type GeoZone, type LayerId } from "@/lib/agent/types";
import MapPreviewCard, { type PreviewData } from "./MapPreviewCard";
import MapUpdateIndicator from "./MapUpdateIndicator";

const ACCENT = "#7C8CFF";

const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export function collectZonePositions(
  zone: GeoZone | null | undefined,
): [number, number][] {
  return collectGeoJsonPositions(zone as GeoJSON.FeatureCollection | null | undefined);
}

export function createPinElement(
  property: { id: string; match_score: number },
  isTop: boolean,
  index = 0,
): HTMLDivElement {
  const element = document.createElement("div");
  element.className = `pin${isTop ? " pin--top" : ""}`;
  element.dataset.pinId = property.id;
  element.dataset.top = String(isTop);
  element.style.setProperty("--pin-index", String(index));
  element.setAttribute("role", "button");
  element.setAttribute("tabindex", "0");
  element.setAttribute("aria-label", `Объект, совпадение ${property.match_score}%`);
  const dot = document.createElement("span");
  dot.className = "pin__dot";
  element.appendChild(dot);
  return element;
}

function listingDot() {
  const element = document.createElement("button");
  element.type = "button";
  element.className = "map-listing-dot";
  element.setAttribute("aria-label", "Открыть объект на карте");
  return element;
}

function densityOpacity(zoom: number) {
  if (zoom >= 15.5) return 0;
  if (zoom <= 11) return 0.22;
  return 0.22 * (15.5 - zoom) / 4.5;
}

function minimumPointZoom(id: LayerId) {
  if (id === "metro") return 10.5;
  if (id === "schools") return 12;
  if (id === "bars") return 14;
  return 12;
}

function pointFeatureInViewport(
  feature: GeoJSON.Feature,
  viewport: [number, number, number, number] | null,
) {
  if (!viewport || !feature.geometry) return true;
  if (feature.geometry.type === "Point") {
    const coordinates = feature.geometry.coordinates as [number, number];
    return isValidLngLat(coordinates) && isInViewport(coordinates, viewport);
  }
  if (feature.geometry.type === "MultiPoint") {
    return feature.geometry.coordinates.some((coordinates) =>
      isValidLngLat(coordinates) && isInViewport(coordinates, viewport));
  }
  return true;
}

export function dataLayerStyle(
  id: LayerId,
  feature: google.maps.Data.Feature,
  zoom: number,
): google.maps.Data.StyleOptions {
  const geometry = feature.getGeometry()?.getType() ?? "Polygon";
  // Линии метро/МЦК/МЦД красятся своим цветом из properties.colour (задача
  // 15), а не единым layerColor("metro") — иначе все линии слились бы в один
  // красный, а МЦК и линии со своей палитрой стали бы неотличимы от метро.
  // Станции (Point) палитру слоя не трогают — их цвет остаётся прежним.
  const isMetroLine = id === "metro" && (geometry === "LineString" || geometry === "MultiLineString");
  const color = isMetroLine
    ? metroLineColor(feature.getProperty("colour") as string | null | undefined)
    : layerColor(id) ?? layerPaintColor(geometry);
  const visible = geometry === "Point" || geometry === "MultiPoint"
    ? zoom >= minimumPointZoom(id)
    : id !== "crime" || zoom < 15.5;
  if (geometry === "Point" || geometry === "MultiPoint") {
    const name = feature.getProperty("name");
    return {
      clickable: false,
      visible,
      icon: {
        path: google.maps.SymbolPath.CIRCLE,
        fillColor: color,
        fillOpacity: 0.94,
        strokeColor: "#ffffff",
        strokeOpacity: 0.95,
        strokeWeight: 1.5,
        scale: id === "metro" ? 6 : 5,
      },
      label: id === "metro" && typeof name === "string" && zoom >= 14.5
        ? { text: name, color: "#4b4f5f", fontSize: "10px", fontWeight: "500" }
        : undefined,
      zIndex: id === "metro" ? 30 : 20,
    };
  }
  if (geometry === "LineString" || geometry === "MultiLineString") {
    return {
      clickable: false,
      visible,
      strokeColor: color,
      strokeOpacity: 0.72,
      strokeWeight: 4,
      zIndex: 10,
    };
  }
  return {
    clickable: false,
    visible,
    fillColor: color,
    fillOpacity: id === "crime" ? densityOpacity(zoom) : 0.25,
    strokeColor: color,
    strokeOpacity: id === "crime" ? 0.18 : 0.46,
    strokeWeight: 1,
    zIndex: id === "crime" ? 1 : 5,
  };
}

export default function MapCanvas() {
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, unavailable, missingKey } = useGoogleMap(container);
  const zone = useSession((state) => state.zoneGeoJSON);
  const properties = useSession((state) => state.properties);
  const hoveredId = useSession((state) => state.hoveredId);
  const setHovered = useSession((state) => state.setHoveredProperty);
  const activeLayers = useSession((state) => state.activeLayers);
  const layerData = useSession((state) => state.layerData);
  const selectProperty = useSession((state) => state.selectProperty);
  const setViewport = useSession((state) => state.setViewport);
  const viewport = useSession((state) => state.viewport);
  const mapUpdating = useSession((state) => state.mapUpdating);
  const setMapUpdating = useSession((state) => state.setMapUpdating);
  const mapListings = useSession((state) => state.mapListings);
  const visibleProperties = useMemo(
    () => rankVisibleProperties(properties, viewport),
    [properties, viewport],
  );

  const resultMarkers = useRef<google.maps.marker.AdvancedMarkerElement[]>([]);
  const listingMarkers = useRef<google.maps.marker.AdvancedMarkerElement[]>([]);
  const clusterer = useRef<MarkerClusterer | null>(null);
  const projection = useRef<google.maps.OverlayView | null>(null);
  const geoLayers = useRef<Partial<Record<LayerId, google.maps.Data>>>({});

  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const [anchor, setAnchor] = useState<{ x: number; y: number } | null>(null);
  const [mapPick, setMapPick] = useState<{
    data: PreviewData;
    lngLat: [number, number];
    anchor: { x: number; y: number };
  } | null>(null);
  const closeTimer = useRef<number | undefined>(undefined);
  const centeredZone = useRef<GeoZone | null>(null);

  const openPreview = useCallback((index: number) => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    setPreviewIndex(index);
  }, []);
  const scheduleClose = useCallback(() => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setPreviewIndex(null), 160);
  }, []);

  useEffect(() => {
    if (!map || !ready) return;
    projection.current = createProjectionOverlay(map);
    return () => {
      projection.current?.setMap(null);
      projection.current = null;
    };
  }, [map, ready]);

  useEffect(() => {
    if (!map || !ready || !zone) return;
    const positions = collectZonePositions(zone);
    if (!positions.length) return;

    const layer = new google.maps.Data({ map });
    replaceDataLayer(layer, zone as unknown as GeoJSON.FeatureCollection);
    const reduced = prefersReducedMotion();
    const hidden: google.maps.Data.StyleOptions = {
      fillColor: ACCENT,
      fillOpacity: reduced ? 0.18 : 0,
      strokeColor: ACCENT,
      strokeOpacity: reduced ? 1 : 0,
      strokeWeight: 2.5,
      clickable: false,
      zIndex: 2,
    };
    layer.setStyle(hidden);

    const reveal = () => layer.setStyle({ ...hidden, fillOpacity: 0.18, strokeOpacity: 1 });
    const listener = reduced ? null : google.maps.event.addListenerOnce(map, "idle", reveal);
    if (reduced) reveal();

    return () => {
      listener?.remove();
      layer.setMap(null);
    };
  }, [map, ready, zone]);

  useEffect(() => {
    if (!map || !ready || !zone || centeredZone.current === zone) return;
    centeredZone.current = zone;
    const resultPositions = [...useSession.getState().properties]
      .filter((property) => isValidLngLat(property.coordinates))
      .sort((a, b) => b.match_score - a.match_score)
      .slice(0, 10)
      .map((property) => property.coordinates);
    const positions = resultPositions.length
      ? resultPositions
      : collectZonePositions(zone).filter(isValidLngLat);
    if (!positions.length) return;

    map.setHeading(0);
    map.setTilt(0);
    map.fitBounds(createLatLngBounds(positions), 72);
    const capZoom = google.maps.event.addListenerOnce(map, "idle", () => {
      if ((map.getZoom() ?? 0) > 14) map.setZoom(14);
    });
    return () => capZoom.remove();
  }, [map, ready, zone]);

  useEffect(() => {
    if (!map || !ready) return;
    resultMarkers.current.forEach(removeAdvancedMarker);
    setPreviewIndex(null);
    const topId = visibleProperties[0]?.property.id;

    resultMarkers.current = visibleProperties.map(({ property, sourceIndex }, index) => {
      const element = createPinElement(property, property.id === topId, index);
      element.addEventListener("mouseenter", () => { setHovered(property.id); openPreview(sourceIndex); });
      element.addEventListener("mouseleave", () => { setHovered(null); scheduleClose(); });
      element.addEventListener("focus", () => { setHovered(property.id); openPreview(sourceIndex); });
      element.addEventListener("blur", () => { setHovered(null); scheduleClose(); });
      element.addEventListener("click", (event) => { event.stopPropagation(); selectProperty(sourceIndex); });
      element.addEventListener("keydown", (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          selectProperty(sourceIndex);
        }
      });
      return new google.maps.marker.AdvancedMarkerElement({
        map,
        position: toLatLng(property.coordinates),
        content: element,
        title: property.address || property.name,
      });
    });

    return () => {
      resultMarkers.current.forEach(removeAdvancedMarker);
      resultMarkers.current = [];
    };
  }, [map, ready, visibleProperties, setHovered, openPreview, scheduleClose, selectProperty]);

  useEffect(() => {
    resultMarkers.current.forEach((marker) => {
      const element = marker.content as HTMLElement | null;
      element?.classList.toggle("pin--active", element.dataset.pinId === hoveredId);
    });
  }, [hoveredId]);

  useEffect(() => {
    if (!map || !ready) return;
    let timer: number | undefined;
    const publish = () => {
      const bounds = map.getBounds()?.toJSON();
      if (bounds) setViewport(boundsLiteralToViewport(bounds));
    };
    const schedulePublish = () => {
      if (timer) window.clearTimeout(timer);
      setMapUpdating(true);
      timer = window.setTimeout(publish, 300);
    };
    const drag = map.addListener("dragstart", () => setMapUpdating(true));
    const zoom = map.addListener("zoom_changed", () => setMapUpdating(true));
    const idle = map.addListener("idle", schedulePublish);
    schedulePublish();
    return () => {
      if (timer) window.clearTimeout(timer);
      drag.remove();
      zoom.remove();
      idle.remove();
      useSession.getState().setMapUpdating(false);
    };
  }, [map, ready, setMapUpdating, setViewport]);

  useEffect(() => {
    if (!map || !ready) return;
    const close = map.addListener("click", () => setMapPick(null));
    return () => close.remove();
  }, [map, ready]);

  useEffect(() => {
    if (!map || !ready) return;
    clusterer.current?.clearMarkers();
    listingMarkers.current.forEach(removeAdvancedMarker);
    listingMarkers.current = [];

    for (const feature of mapListings?.features ?? []) {
      if (feature.geometry?.type !== "Point") continue;
      const coordinates = feature.geometry.coordinates as [number, number];
      if (!isValidLngLat(coordinates) || (viewport && !isInViewport(coordinates, viewport))) continue;
      const properties = (feature.properties ?? {}) as Record<string, unknown>;
      const element = listingDot();
      const marker = new google.maps.marker.AdvancedMarkerElement({
        position: toLatLng(coordinates),
        content: element,
        title: String(properties.address ?? properties.name ?? "Объект"),
      });
      element.addEventListener("click", (event) => {
        event.stopPropagation();
        const projected = projection.current ? projectLngLat(projection.current, coordinates) : null;
        if (!projected) return;
        setMapPick({
          data: {
            id: String(properties.id),
            name: String(properties.name ?? ""),
            address: typeof properties.address === "string" ? properties.address : "",
            cover_image: String(properties.cover_image ?? ""),
            price_from: typeof properties.price === "number" ? properties.price : null,
          },
          lngLat: coordinates,
          anchor: projected,
        });
      });
      listingMarkers.current.push(marker);
    }

    clusterer.current = new MarkerClusterer({ map, markers: listingMarkers.current });
    return () => {
      clusterer.current?.clearMarkers();
      clusterer.current = null;
      listingMarkers.current.forEach(removeAdvancedMarker);
      listingMarkers.current = [];
    };
  }, [map, ready, mapListings, viewport]);

  useEffect(() => {
    if (!map || !ready) return;
    Object.values(geoLayers.current).forEach((layer) => layer?.setMap(null));
    geoLayers.current = {};

    for (const id of RENDERED_LAYER_IDS) {
      const data = layerData[id];
      if (!activeLayers[id] || !data?.features.length) continue;
      const layer = new google.maps.Data({ map });
      layer.addGeoJson({
        ...data,
        features: data.features.filter((feature) => pointFeatureInViewport(feature, viewport)),
      } as GeoJSON.GeoJsonObject);
      layer.setStyle((feature) => dataLayerStyle(id, feature, map.getZoom() ?? 12));
      geoLayers.current[id] = layer;
    }

    const zoom = map.addListener("zoom_changed", () => {
      for (const id of RENDERED_LAYER_IDS) {
        const layer = geoLayers.current[id];
        if (layer) {
          layer.setStyle((feature) => dataLayerStyle(id, feature, map.getZoom() ?? 12));
        }
      }
    });
    return () => {
      zoom.remove();
      Object.values(geoLayers.current).forEach((layer) => layer?.setMap(null));
      geoLayers.current = {};
    };
  }, [map, ready, activeLayers, layerData, viewport]);

  useEffect(() => {
    if (!map || !ready || !projection.current) return;
    const update = () => {
      if (previewIndex != null) {
        const property = properties[previewIndex];
        if (property) setAnchor(projectLngLat(projection.current!, property.coordinates));
      }
      setMapPick((current) => {
        if (!current) return null;
        const next = projectLngLat(projection.current!, current.lngLat);
        return next ? { ...current, anchor: next } : current;
      });
    };
    update();
    const listener = map.addListener("bounds_changed", update);
    return () => listener.remove();
  }, [map, ready, previewIndex, properties]);

  if (unavailable) {
    return (
      <div
        data-testid="map-missing-key"
        className="grid h-full w-full place-items-center rounded-3xl bg-[#f6f7fb] px-6 text-center text-sm text-zinc-400"
      >
        {missingKey
          ? "Добавьте Google Maps API key и Map ID в .env"
          : "Google Maps не удалось загрузить"}
      </div>
    );
  }

  return (
    <div className="relative h-full w-full">
      <div ref={container} data-testid="map-canvas" className="absolute inset-0 overflow-hidden rounded-3xl" />
      <MapUpdateIndicator visible={mapUpdating} />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 rounded-3xl shadow-[inset_0_0_0_1px_rgba(20,20,34,0.06),inset_0_1px_0_rgba(255,255,255,0.4)]"
      />
      <div className="pointer-events-none absolute inset-0 overflow-hidden rounded-3xl">
        <AnimatePresence>
          {previewIndex != null && anchor && properties[previewIndex] && (
            <MapPreviewCard
              data={{
                id: properties[previewIndex].id,
                name: properties[previewIndex].name,
                address: properties[previewIndex].address,
                cover_image: properties[previewIndex].cover_image,
                price_from: properties[previewIndex].price_from,
                match_score: properties[previewIndex].match_score,
                tags: properties[previewIndex].tags,
              }}
              anchor={anchor}
              onOpen={() => selectProperty(previewIndex)}
              onMouseEnter={() => openPreview(previewIndex)}
              onMouseLeave={scheduleClose}
              onClose={() => setPreviewIndex(null)}
            />
          )}
          {mapPick && (
            <MapPreviewCard
              data={mapPick.data}
              anchor={mapPick.anchor}
              onOpen={() => {
                useSession.getState().openListingFromMap({
                  id: mapPick.data.id,
                  name: mapPick.data.name,
                  address: mapPick.data.address ?? "",
                  cover_image: mapPick.data.cover_image,
                  match_score: 0,
                  price_from: mapPick.data.price_from,
                  rooms: null,
                  area_sqm: null,
                  floor: "",
                  tags: [],
                  coordinates: mapPick.lngLat,
                });
              }}
              onClose={() => setMapPick(null)}
            />
          )}
        </AnimatePresence>
      </div>
    </div>
  );
}
