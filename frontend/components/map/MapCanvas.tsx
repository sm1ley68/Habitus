"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import maplibregl from "maplibre-gl";
import { AnimatePresence } from "framer-motion";
import { useMaplibre } from "@/lib/map/useMaplibre";
import { layerPaintColor } from "@/lib/map/style";
import { useSession } from "@/lib/store/session";
import { MAP_LAYER_IDS, type GeoZone } from "@/lib/agent/types";
import { DUR } from "@/lib/motion";
import MapPreviewCard, { type PreviewData } from "./MapPreviewCard";

// Periwinkle glow — the ONLY saturated brand color allowed on the neutral canvas.
const ACCENT = "#7C8CFF";

// ease-out-expo: a hard early acceleration that then settles, so the camera
// "arrives" and comes to rest like a real crane shot rather than gliding at a
// constant speed. Passed straight to MapLibre's animation loop.
const easeOutExpo = (t: number): number =>
  t >= 1 ? 1 : 1 - Math.pow(2, -10 * t);

const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

// [paint-property, visible-opacity] pairs for a geometry type. Toggling a layer
// crossfades every listed property between its visible value and 0.
function layerOpacityProps(geometryType: string): Array<[string, number]> {
  if (geometryType === "Point")
    return [["circle-opacity", 0.9], ["circle-stroke-opacity", 0.85]];
  if (geometryType === "LineString") return [["line-opacity", 0.7]];
  return [["fill-opacity", 0.25]];
}

/**
 * Every [lng, lat] position in a zone, flattened. Robust to Polygon AND
 * MultiPolygon (okrug unions come back as MultiPolygon) and to a collection
 * with several features — the camera should frame all of them, not just the
 * first. An absent, empty or malformed zone yields an empty array, which the
 * caller treats as "nothing to draw" instead of crashing on features[0].
 */
export function collectZonePositions(
  zone: GeoZone | null | undefined,
): [number, number][] {
  const positions: [number, number][] = [];
  const collect = (node: unknown): void => {
    if (!Array.isArray(node)) return;
    if (typeof node[0] === "number" && typeof node[1] === "number") {
      positions.push([node[0], node[1]]);
      return;
    }
    node.forEach(collect);
  };
  for (const feature of zone?.features ?? []) collect(feature?.geometry?.coordinates);
  return positions;
}

/**
 * Pure, unit-testable factory for a property pin's DOM. MapLibre positions the
 * returned wrapper with a translate transform, so every visual transform
 * (entrance, hover scale, pulse) lives on the inner `.pin__dot` and never
 * fights that positioning. `index` drives a staggered entrance via CSS var.
 */
export function createPinElement(
  p: { id: string; match_score: number },
  isTop: boolean,
  index = 0,
): HTMLDivElement {
  const el = document.createElement("div");
  el.className = `pin${isTop ? " pin--top" : ""}`;
  el.dataset.pinId = p.id;
  el.dataset.top = String(isTop);
  el.style.setProperty("--pin-index", String(index));
  el.setAttribute("role", "button");
  el.setAttribute("tabindex", "0");
  el.setAttribute("aria-label", `Объект, совпадение ${p.match_score}%`);
  const dot = document.createElement("span");
  dot.className = "pin__dot";
  el.appendChild(dot);
  return el;
}

export default function MapCanvas() {
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, missingKey } = useMaplibre(container);
  const zone = useSession((s) => s.zoneGeoJSON);
  const properties = useSession((s) => s.properties);
  const hoveredId = useSession((s) => s.hoveredId);
  const setHovered = useSession((s) => s.setHoveredProperty);
  const activeLayers = useSession((s) => s.activeLayers);
  const layerData = useSession((s) => s.layerData);
  const selectProperty = useSession((s) => s.selectProperty);
  const setViewport = useSession((s) => s.setViewport);
  const mapListings = useSession((s) => s.mapListings);
  // Превью объявления, выбранного на карте: сама фича + её точка на экране.
  const [mapPick, setMapPick] = useState<{
    data: PreviewData; lngLat: [number, number]; anchor: { x: number; y: number };
  } | null>(null);
  const markers = useRef<maplibregl.Marker[]>([]);
  const pendingRemoval = useRef<Record<string, number>>({});


  // Hover preview: which property is previewed + its projected pixel anchor. A
  // short close delay lets the cursor travel from the pin onto the card to click.
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const [anchor, setAnchor] = useState<{ x: number; y: number } | null>(null);
  const closeTimer = useRef<number | undefined>(undefined);

  const openPreview = useCallback((i: number) => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    setPreviewIndex(i);
  }, []);
  const scheduleClose = useCallback(() => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setPreviewIndex(null), 160);
  }, []);

  // Cinematic camera + investigation-style zone reveal.
  useEffect(() => {
    if (!map || !ready || !zone) return;
    const reduced = prefersReducedMotion();
    const revealMs = reduced ? 0 : 900;

    // Пустая зона — валидный ответ бэка, а не ошибка: контракт (§3 «Обработка
    // пустых результатов») велит слать suggested_areas_geojson всегда, а строить
    // его не из чего, когда в запросе не было области и ничего не нашлось.
    // Рисовать нечего и подгонять камеру не по чему — гасим прошлую зону
    // и оставляем карту как есть.
    const positions = collectZonePositions(zone);
    if (!positions.length) {
      if (map.getLayer("zone-fill")) map.setPaintProperty("zone-fill", "fill-opacity", 0);
      if (map.getLayer("zone-line")) map.setPaintProperty("zone-line", "line-opacity", 0);
      return;
    }

    if (!map.getSource("zone")) {
      map.addSource("zone", { type: "geojson", data: zone as unknown as GeoJSON.FeatureCollection });
      // Fill washes in first (the area is "uncovered")...
      map.addLayer({
        id: "zone-fill", type: "fill", source: "zone",
        paint: {
          "fill-color": ACCENT,
          "fill-opacity": 0,
          "fill-opacity-transition": { duration: revealMs, delay: 0 },
        },
      });
      // ...then the outline firms up a beat later, framing what was found.
      map.addLayer({
        id: "zone-line", type: "line", source: "zone",
        layout: { "line-join": "round", "line-cap": "round" },
        paint: {
          "line-color": ACCENT,
          "line-width": 2.5,
          "line-blur": 0.4,
          "line-opacity": 0,
          "line-opacity-transition": { duration: revealMs, delay: reduced ? 0 : 260 },
        },
      });
    } else {
      (map.getSource("zone") as maplibregl.GeoJSONSource).setData(zone as unknown as GeoJSON.FeatureCollection);
    }

    const bounds = positions.reduce(
      (b, c) => b.extend(c),
      new maplibregl.LngLatBounds(positions[0], positions[0]),
    );

    const reveal = () => {
      map.setPaintProperty("zone-fill", "fill-opacity", 0.18);
      map.setPaintProperty("zone-line", "line-opacity", 1);
    };

    if (reduced) {
      map.fitBounds(bounds, { padding: 90, duration: 0, pitch: 0, bearing: 0 });
      reveal();
      return;
    }

    map.fitBounds(bounds, {
      padding: { top: 96, bottom: 96, left: 96, right: 96 },
      duration: DUR.cinematic * 1000 + 500, // ~1.7s filmic settle
      pitch: 0,           // top-down: зона читается как явная подсвеченная область
      bearing: 0,         // север сверху — чистое обрамление найденной зоны
      easing: easeOutExpo,
    });
    // Uncover the zone only once the camera has come to rest.
    map.once("moveend", reveal);
  }, [map, ready, zone]);

  // Property pins — the top match (highest score) pulses; the rest cascade in.
  useEffect(() => {
    if (!map || !ready) return;
    markers.current.forEach((m) => m.remove());
    setPreviewIndex(null);
    const topId = [...properties].sort((a, b) => b.match_score - a.match_score)[0]?.id;
    markers.current = properties.map((p, i) => {
      const el = createPinElement(p, p.id === topId, i);
      el.addEventListener("mouseenter", () => { setHovered(p.id); openPreview(i); });
      el.addEventListener("mouseleave", () => { setHovered(null); scheduleClose(); });
      el.addEventListener("focus", () => { setHovered(p.id); openPreview(i); });
      el.addEventListener("blur", () => { setHovered(null); scheduleClose(); });
      el.addEventListener("click", () => selectProperty(i));
      el.addEventListener("keydown", (e) => {
        if ((e as KeyboardEvent).key === "Enter" || (e as KeyboardEvent).key === " ") {
          e.preventDefault();
          selectProperty(i);
        }
      });
      return new maplibregl.Marker({ element: el }).setLngLat(p.coordinates).addTo(map);
    });
    return () => markers.current.forEach((m) => m.remove());
  }, [map, ready, properties, setHovered, openPreview, scheduleClose, selectProperty]);

  // Превью объекта карты держится за своей точкой при панораме и зуме.
  useEffect(() => {
    if (!map || !mapPick) return;
    const update = () =>
      setMapPick((cur) => (cur ? { ...cur, anchor: map.project(cur.lngLat) } : cur));
    map.on("move", update);
    return () => { map.off("move", update); };
  }, [map, mapPick]);

  // Keep the preview card pinned to its marker as the camera pans/zooms.
  useEffect(() => {
    if (!map || previewIndex == null) return;
    const p = properties[previewIndex];
    if (!p) return;
    const update = () => setAnchor(map.project(p.coordinates));
    update();
    map.on("move", update);
    return () => { map.off("move", update); };
  }, [map, previewIndex, properties]);

  // Границы вьюпорта уезжают в store: evidence-слои (communal/noise/crime)
  // без bbox приходят пустыми, поэтому карта обязана сообщить, что видно.
  useEffect(() => {
    if (!map || !ready) return;
    const publish = () => {
      const b = map.getBounds();
      setViewport([b.getWest(), b.getSouth(), b.getEast(), b.getNorth()]);
    };
    publish();
    map.on("moveend", publish);
    return () => { map.off("moveend", publish); };
  }, [map, ready, setViewport]);

  // Слой ВСЕХ объявлений под вьюпортом — отдельно от маркеров выдачи. Кликом по
  // точке открывается паспорт любого объекта, а не только попавшего в подбор.
  // Кластеризация нативная: под вьюпорт приходит до 2000 точек, россыпью они
  // кладут кадры и перекрывают маркеры результатов.
  //
  // Источник и обработчики создаются ОДИН раз, данные обновляются отдельным
  // эффектом: mapListings меняется на каждом moveend, и будь всё в одном эффекте,
  // cleanup снимал бы обработчики, а повторный проход уходил бы в ранний return
  // по уже существующему источнику — клики переставали бы работать после первого
  // же сдвига карты.
  useEffect(() => {
    if (!map || !ready) return;
    const SRC = "all-listings";
    if (map.getSource(SRC)) return;

    map.addSource(SRC, {
      type: "geojson",
      data: { type: "FeatureCollection", features: [] },
      cluster: true, clusterRadius: 55, clusterMaxZoom: 15,
    });
    map.addLayer({
      id: `${SRC}-clusters`, type: "circle", source: SRC, filter: ["has", "point_count"],
      paint: {
        // Плотность кодируется и размером, и насыщенностью — на светлой подложке
        // dataviz-light одного размера мало.
        "circle-color": ["step", ["get", "point_count"], "#8b8fa3", 25, "#6c718a", 100, "#4b5069"],
        "circle-opacity": 0.9,
        "circle-radius": ["step", ["get", "point_count"], 13, 25, 17, 100, 22],
        "circle-stroke-width": 2,
        "circle-stroke-color": "#ffffff",
        "circle-stroke-opacity": 0.9,
      },
    });
    // Подписи числа в кластере намеренно нет: symbol-слой требует glyphs в
    // стиле карты, а dataviz-light их не обязан отдавать — упавший слой рушит
    // весь эффект и с ним остальные слои. Размер круга и так кодирует объём.
    map.addLayer({
      id: `${SRC}-point`, type: "circle", source: SRC, filter: ["!", ["has", "point_count"]],
      paint: {
        // Тот же язык, что у пинов выдачи (.pin__dot): светлое ядро в белом
        // гало. Приглушённее результатов подбора — это фон карты, и он не
        // должен спорить с выдачей за внимание.
        "circle-color": "#6c718a",
        "circle-opacity": 0.85,
        "circle-radius": ["interpolate", ["linear"], ["zoom"], 10, 3.5, 14, 6, 17, 9],
        "circle-stroke-width": ["interpolate", ["linear"], ["zoom"], 10, 1, 14, 2],
        "circle-stroke-color": "#ffffff",
        "circle-stroke-opacity": 0.95,
      },
    });

    const openPassport = (e: maplibregl.MapLayerMouseEvent) => {
      const f = e.features?.[0];
      if (!f) return;
      const p = f.properties as Record<string, unknown>;
      const [lng, lat] = (f.geometry as GeoJSON.Point).coordinates;
      // Клик открывает превью прямо на карте, а не уводит на страницу паспорта:
      // на страницу ведёт уже кнопка внутри карточки.
      setMapPick({
        data: {
          id: String(p.id), name: String(p.name ?? ""),
          address: typeof p.address === "string" ? p.address : "",
          cover_image: String(p.cover_image ?? ""),
          price_from: typeof p.price === "number" ? p.price : null,
          // match_score НЕ передаём: вне подбора его не существует.
        },
        lngLat: [lng, lat],
        anchor: map.project([lng, lat]),
      });
    };
    const zoomCluster = (e: maplibregl.MapLayerMouseEvent) => {
      const f = e.features?.[0];
      if (!f) return;
      map.easeTo({
        center: (f.geometry as GeoJSON.Point).coordinates as [number, number],
        zoom: map.getZoom() + 2,
      });
    };
    const cursor = (v: string) => () => { map.getCanvas().style.cursor = v; };

    map.on("click", `${SRC}-point`, openPassport);
    // Клик мимо точки — закрыть превью. Слой-специфичный обработчик отработает
    // раньше и успеет поставить новое, поэтому порядок безопасен.
    map.on("click", (e: maplibregl.MapMouseEvent) => {
      const hit = map.queryRenderedFeatures(e.point, { layers: [`${SRC}-point`] });
      if (!hit.length) setMapPick(null);
    });
    map.on("click", `${SRC}-clusters`, zoomCluster);
    map.on("mouseenter", `${SRC}-point`, cursor("pointer"));
    map.on("mouseleave", `${SRC}-point`, cursor(""));
    map.on("mouseenter", `${SRC}-clusters`, cursor("pointer"));
    map.on("mouseleave", `${SRC}-clusters`, cursor(""));
  }, [map, ready]);

  // Данные слоя объявлений — отдельно от его создания (см. комментарий выше).
  useEffect(() => {
    if (!map || !ready || !mapListings) return;
    const src = map.getSource("all-listings") as maplibregl.GeoJSONSource | undefined;
    src?.setData(mapListings);
  }, [map, ready, mapListings]);

  // Card <-> pin cross-highlight: scale up whichever pin the store says is hovered.
  useEffect(() => {
    markers.current.forEach((m) => {
      const el = m.getElement();
      el.classList.toggle("pin--active", el.dataset.pinId === hoveredId);
    });
  }, [hoveredId]);

  // Toggleable geo layers — each renders as its own source/layer and crossfades
  // its opacity both in and out (fade to 0, then drop the source once faded).
  useEffect(() => {
    if (!map || !ready) return;
    const reduced = prefersReducedMotion();
    const xfade = reduced ? 0 : DUR.base * 1000; // 240ms

    MAP_LAYER_IDS.forEach((id) => {
      const srcId = `layer-${id}`;
      const data = layerData[id];
      // Слой ещё не приехал (или бэк отдал по нему пусто — evidence-слои без
      // bbox и слои без данных приходят пустыми) — рисовать нечего.
      const on = !!activeLayers[id] && !!data?.features.length;
      const geom = data?.features[0]?.geometry.type ?? "Polygon";
      const color = layerPaintColor(geom);
      const props = layerOpacityProps(geom);

      if (on) {
        // Cancel a queued removal if the user re-enabled mid-fade.
        if (pendingRemoval.current[srcId]) {
          clearTimeout(pendingRemoval.current[srcId]);
          delete pendingRemoval.current[srcId];
        }
        if (!map.getSource(srcId)) {
          map.addSource(srcId, { type: "geojson", data });
          if (geom === "Point") {
            map.addLayer({
              id: srcId, type: "circle", source: srcId,
              paint: {
                "circle-radius": 5,
                "circle-color": color,
                "circle-opacity": 0,
                "circle-stroke-color": "#ffffff",
                "circle-stroke-width": 1.5,
                "circle-stroke-opacity": 0,
                "circle-opacity-transition": { duration: xfade, delay: 0 },
                "circle-stroke-opacity-transition": { duration: xfade, delay: 0 },
              },
            });
          } else if (geom === "LineString") {
            map.addLayer({
              id: srcId, type: "line", source: srcId,
              layout: { "line-join": "round", "line-cap": "round" },
              paint: {
                "line-color": color,
                "line-width": 4,
                "line-blur": 0.6,
                "line-opacity": 0,
                "line-opacity-transition": { duration: xfade, delay: 0 },
              },
            });
          } else {
            map.addLayer({
              id: srcId, type: "fill", source: srcId,
              paint: {
                "fill-color": color,
                "fill-opacity": 0,
                "fill-opacity-transition": { duration: xfade, delay: 0 },
              },
            });
          }
        }
        // Next frame so the 0 -> target change animates rather than snapping.
        requestAnimationFrame(() => {
          if (!map.getLayer(srcId)) return;
          props.forEach(([prop, val]) => map.setPaintProperty(srcId, prop, val));
        });
      } else if (map.getLayer(srcId)) {
        props.forEach(([prop]) => map.setPaintProperty(srcId, prop, 0));
        pendingRemoval.current[srcId] = window.setTimeout(() => {
          try {
            if (map.getLayer(srcId)) map.removeLayer(srcId);
            if (map.getSource(srcId)) map.removeSource(srcId);
          } catch {
            /* map may already be torn down */
          }
          delete pendingRemoval.current[srcId];
        }, xfade + 40);
      }
    });
  }, [map, ready, activeLayers, layerData]);

  if (missingKey) {
    return (
      <div
        data-testid="map-missing-key"
        className="w-full h-full rounded-3xl bg-[#f6f7fb] grid place-items-center text-sm text-zinc-400 px-6 text-center"
      >
        Добавьте NEXT_PUBLIC_MAPTILER_KEY в .env.local, чтобы увидеть карту
      </div>
    );
  }

  return (
    <div className="relative w-full h-full">
      <div
        ref={container}
        data-testid="map-canvas"
        className="absolute inset-0 rounded-3xl overflow-hidden"
      />
      {/* Static inner-edge refraction — frames the map in its container. No
          animation, no glow: purely structural depth. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 rounded-3xl shadow-[inset_0_0_0_1px_rgba(20,20,34,0.06),inset_0_1px_0_rgba(255,255,255,0.4)]"
      />

      {/* Hover preview — anchored above the pin. The wrapper ignores pointer
          events so the map stays draggable; only the card is interactive, which
          lets the cursor bridge from pin to card without the popup closing. */}
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

          {/* Превью объявления, выбранного на слое карты (вне подбора). */}
          {mapPick && (
            <MapPreviewCard
              data={mapPick.data}
              anchor={mapPick.anchor}
              onOpen={() => {
                useSession.getState().openListingFromMap({
                  id: mapPick.data.id, name: mapPick.data.name,
                  address: mapPick.data.address ?? "",
                  cover_image: mapPick.data.cover_image,
                  match_score: 0, price_from: mapPick.data.price_from,
                  rooms: null, area_sqm: null, floor: "", tags: [],
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
