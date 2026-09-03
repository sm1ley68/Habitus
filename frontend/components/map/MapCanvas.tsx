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
  isValidLngLat,
  rankVisibleProperties,
  viewportChangedEnough,
} from "@/lib/map/viewport";
import { minimumPointZoom, zoomStyleKey } from "@/lib/map/layers";
import { diffKeys, featureKey, keyById, keyed } from "@/lib/map/sync";
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
  property: { id: string; match_score: number | null },
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
  // Без процента совпадения подпись не врёт нулём, а просто его не называет.
  element.setAttribute("aria-label",
    typeof property.match_score === "number"
      ? `Объект, совпадение ${property.match_score}%`
      : "Объект");
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

  // Маркеры и фичи держатся между обновлениями и диффятся по ключу: панорама
  // почти всегда показывает те же объекты, а пересборка с нуля стоила ~8800
  // фич и 2000 DOM-узлов на каждый шаг зума.
  const resultMarkers = useRef(new Map<string, {
    marker: google.maps.marker.AdvancedMarkerElement;
    sourceIndex: number;
  }>());
  const listingMarkers = useRef(new Map<string, google.maps.marker.AdvancedMarkerElement>());
  const clusterer = useRef<MarkerClusterer | null>(null);
  const projection = useRef<google.maps.OverlayView | null>(null);
  const geoLayers = useRef(new Map<LayerId, {
    layer: google.maps.Data;
    features: Map<string, google.maps.Data.Feature>;
    styleKey: string;
  }>());

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
      .sort((a, b) => (b.match_score ?? -1) - (a.match_score ?? -1))
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
    const topId = visibleProperties[0]?.property.id;
    const next = new Map(visibleProperties.map((item) => [item.property.id, item]));

    // Ушедшие из выдачи или сменившие место в исходном массиве (слушатели
    // замкнуты на sourceIndex) — снимаются. Остальные переживают панораму.
    for (const [id, entry] of resultMarkers.current) {
      const item = next.get(id);
      if (!item || item.sourceIndex !== entry.sourceIndex) {
        removeAdvancedMarker(entry.marker);
        resultMarkers.current.delete(id);
      }
    }
    // Превью закрывается, только если его объект действительно исчез с карты.
    setPreviewIndex((current) => {
      if (current == null) return current;
      const shown = properties[current];
      return shown && next.has(shown.id) ? current : null;
    });

    visibleProperties.forEach(({ property, sourceIndex }, index) => {
      const isTop = property.id === topId;
      const existing = resultMarkers.current.get(property.id);
      if (existing) {
        const element = existing.marker.content as HTMLElement | null;
        if (element) {
          element.classList.toggle("pin--top", isTop);
          element.dataset.top = String(isTop);
        }
        return;
      }
      const element = createPinElement(property, isTop, index);
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
      resultMarkers.current.set(property.id, {
        sourceIndex,
        marker: new google.maps.marker.AdvancedMarkerElement({
          map,
          position: toLatLng(property.coordinates),
          content: element,
          title: property.address || property.name,
        }),
      });
    });
  }, [map, ready, properties, visibleProperties, setHovered, openPreview, scheduleClose, selectProperty]);

  // Снятие маркеров — отдельным эффектом на размонтирование карты, а не в
  // cleanup эффекта выше: тот теперь переживает смену данных.
  useEffect(() => {
    if (!map || !ready) return;
    const markers = resultMarkers.current;
    return () => {
      markers.forEach(({ marker }) => removeAdvancedMarker(marker));
      markers.clear();
    };
  }, [map, ready]);

  useEffect(() => {
    resultMarkers.current.forEach(({ marker }) => {
      const element = marker.content as HTMLElement | null;
      element?.classList.toggle("pin--active", element.dataset.pinId === hoveredId);
    });
  }, [hoveredId]);

  useEffect(() => {
    if (!map || !ready) return;
    let timer: number | undefined;
    const publish = () => {
      const bounds = map.getBounds()?.toJSON();
      if (!bounds) return;
      const next = boundsLiteralToViewport(bounds);
      const zoom = map.getZoom();
      const { viewport: previous, zoom: previousZoom } = useSession.getState();
      // Микросдвиг после idle не стоит полного цикла «перекачать объявления и
      // все активные слои» — это мегабайты на каждое дрожание карты.
      if (previousZoom === (zoom ?? null) && !viewportChangedEnough(previous, next)) {
        setMapUpdating(false);
        return;
      }
      setViewport(next, zoom ?? undefined);
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
    if (!clusterer.current) clusterer.current = new MarkerClusterer({ map, markers: [] });
    const cluster = clusterer.current;

    // Фильтрации по вьюпорту здесь больше нет: bbox уже применён на сервере, а
    // клиентский фильтр только заставлял пересобирать все маркеры на панораму.
    const next = keyById((mapListings?.features ?? []) as GeoJSON.Feature[]);
    const { added, removed } = diffKeys(listingMarkers.current.keys(), next.keys());
    if (!added.length && !removed.length) return;

    const goneMarkers = removed
      .map((id) => listingMarkers.current.get(id))
      .filter((marker): marker is google.maps.marker.AdvancedMarkerElement => !!marker);
    removed.forEach((id) => listingMarkers.current.delete(id));

    const newMarkers: google.maps.marker.AdvancedMarkerElement[] = [];
    for (const id of added) {
      const feature = next.get(id);
      const geometry = feature?.geometry;
      if (!feature || geometry?.type !== "Point") continue;
      const coordinates = geometry.coordinates as [number, number];
      if (!isValidLngLat(coordinates)) continue;
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
      listingMarkers.current.set(id, marker);
      newMarkers.push(marker);
    }

    // Кластеризатор переиспользуется: раньше на каждое движение карты
    // создавался новый, а он пересчитывает всё дерево кластеров с нуля.
    if (goneMarkers.length) cluster.removeMarkers(goneMarkers, true);
    if (newMarkers.length) cluster.addMarkers(newMarkers, true);
    cluster.render();
  }, [map, ready, mapListings]);

  useEffect(() => {
    if (!map || !ready) return;
    const markers = listingMarkers.current;
    return () => {
      clusterer.current?.clearMarkers();
      clusterer.current = null;
      markers.forEach(removeAdvancedMarker);
      markers.clear();
    };
  }, [map, ready]);

  useEffect(() => {
    if (!map || !ready) return;

    for (const id of RENDERED_LAYER_IDS) {
      const data = layerData[id];
      const entry = geoLayers.current.get(id);

      // Слой выключен или пуст — снимаем с карты, но инстанс не выбрасываем:
      // повторное включение тогда не строит Data-слой заново.
      if (!activeLayers[id] || !data?.features.length) {
        if (entry) {
          entry.features.forEach((feature) => entry.layer.remove(feature));
          entry.features.clear();
          entry.layer.setMap(null);
        }
        continue;
      }

      const current = entry ?? (() => {
        const layer = new google.maps.Data();
        const created = { layer, features: new Map<string, google.maps.Data.Feature>(), styleKey: "" };
        geoLayers.current.set(id, created);
        return created;
      })();
      current.layer.setMap(map);

      // Диф по ключу фичи: панорама внутри уже загруженной области не трогает
      // ни одной фичи, вместо addGeoJson по тысячам точек на каждый шаг.
      const next = keyed(data.features as GeoJSON.Feature[], featureKey);
      const { added, removed } = diffKeys(current.features.keys(), next.keys());
      for (const key of removed) {
        const feature = current.features.get(key);
        if (feature) current.layer.remove(feature);
        current.features.delete(key);
      }
      if (added.length) {
        const created = current.layer.addGeoJson({
          type: "FeatureCollection",
          features: added.map((key) => next.get(key)!),
        } as GeoJSON.GeoJsonObject);
        created.forEach((feature, index) => current.features.set(added[index], feature));
      }

      const styleKey = zoomStyleKey(id, map.getZoom() ?? 12);
      if (current.styleKey !== styleKey) {
        current.styleKey = styleKey;
        current.layer.setStyle((feature) => dataLayerStyle(id, feature, map.getZoom() ?? 12));
      }
    }
  }, [map, ready, activeLayers, layerData]);

  // Перестиль — только при переходе через границу зума, на которой стиль
  // реально меняется: setStyle прогоняет стайлер по КАЖДОЙ фиче слоя.
  useEffect(() => {
    if (!map || !ready) return;
    const listener = map.addListener("zoom_changed", () => {
      const zoom = map.getZoom() ?? 12;
      for (const [id, entry] of geoLayers.current) {
        const styleKey = zoomStyleKey(id, zoom);
        if (entry.styleKey === styleKey) continue;
        entry.styleKey = styleKey;
        entry.layer.setStyle((feature) => dataLayerStyle(id, feature, map.getZoom() ?? 12));
      }
    });
    return () => listener.remove();
  }, [map, ready]);

  useEffect(() => {
    if (!map || !ready) return;
    const layers = geoLayers.current;
    return () => {
      layers.forEach((entry) => entry.layer.setMap(null));
      layers.clear();
    };
  }, [map, ready]);

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
