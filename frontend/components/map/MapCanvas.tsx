"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import maplibregl from "maplibre-gl";
import { AnimatePresence, motion, useReducedMotion } from "framer-motion";
import { useMaplibre } from "@/lib/map/useMaplibre";
import { layerPaintColor } from "@/lib/map/style";
import { useSession } from "@/lib/store/session";
import { MAP_LAYER_IDS } from "@/lib/agent/types";
import { DUR, SPRING } from "@/lib/motion";
import MatchScore from "@/components/result/MatchScore";
import { money } from "@/lib/format";

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
  const markers = useRef<maplibregl.Marker[]>([]);
  const pendingRemoval = useRef<Record<string, number>>({});
  const reduce = useReducedMotion();

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
          "line-width": 2,
          "line-blur": 0.4,
          "line-opacity": 0,
          "line-opacity-transition": { duration: revealMs, delay: reduced ? 0 : 260 },
        },
      });
    } else {
      (map.getSource("zone") as maplibregl.GeoJSONSource).setData(zone as unknown as GeoJSON.FeatureCollection);
    }

    // Fit the camera to the whole zone ring.
    const coords = zone.features[0].geometry.coordinates[0] as [number, number][];
    const bounds = coords.reduce(
      (b, c) => b.extend(c),
      new maplibregl.LngLatBounds(coords[0], coords[0]),
    );

    const reveal = () => {
      map.setPaintProperty("zone-fill", "fill-opacity", 0.12);
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
      pitch: 38,          // slight tilt gives the canvas depth
      bearing: -6,        // matches the initial framing
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
      // Слой ещё не приехал (или бэк отдал по нему пусто — communal/noise/
      // ecology не имеют источника) — рисовать нечего.
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
            <motion.button
              key={properties[previewIndex].id}
              type="button"
              onClick={() => selectProperty(previewIndex)}
              onMouseEnter={() => openPreview(previewIndex)}
              onMouseLeave={scheduleClose}
              onKeyDown={(e) => { if (e.key === "Escape") setPreviewIndex(null); }}
              initial={reduce ? { opacity: 0 } : { opacity: 0, y: 6, scale: 0.97 }}
              animate={reduce ? { opacity: 1 } : { opacity: 1, y: 0, scale: 1 }}
              exit={reduce ? { opacity: 0 } : { opacity: 0, y: 4, scale: 0.98 }}
              transition={reduce ? { duration: DUR.fast } : SPRING.soft}
              style={{
                left: anchor.x,
                top: anchor.y,
                transformOrigin: "bottom center",
                translate: "-50% calc(-100% - 18px)",
              }}
              className="pointer-events-auto absolute block w-56 overflow-hidden rounded-2xl border border-zinc-200 bg-white text-left shadow-[0_18px_40px_-20px_rgba(28,29,32,0.45)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              aria-label={`${properties[previewIndex].name} — открыть карточку`}
            >
              <div className="relative w-full aspect-[3/2] overflow-hidden bg-zinc-100">
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={properties[previewIndex].cover_image}
                  alt={properties[previewIndex].name}
                  className="absolute inset-0 h-full w-full object-cover"
                />
                <div className="absolute inset-0 bg-gradient-to-t from-black/45 via-black/0 to-black/5" />
                <div className="absolute right-2.5 top-2.5">
                  <MatchScore value={properties[previewIndex].match_score} />
                </div>
              </div>
              <div className="p-3.5">
                <h3 className="text-sm font-medium tracking-tight text-[#1c1d20]">
                  {properties[previewIndex].name}
                </h3>
                <p className="mt-1 font-mono text-sm text-zinc-700">
                  {money(properties[previewIndex].price_from)}
                </p>
                <div className="mt-2 flex flex-wrap gap-1.5">
                  {properties[previewIndex].tags.slice(0, 2).map((t) => (
                    <span key={t} className="rounded-md bg-zinc-100 px-2 py-1 text-[11px] text-zinc-600">
                      {t}
                    </span>
                  ))}
                </div>
                <span className="mt-3 inline-flex items-center gap-1 text-xs font-medium text-accent">
                  Открыть карточку
                  <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" fill="none">
                    <path d="M2.5 6h7M6.5 3l3 3-3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                </span>
              </div>
              {/* Pointer notch aiming at the pin. */}
              <span
                aria-hidden
                className="absolute left-1/2 top-full h-3 w-3 -translate-x-1/2 -translate-y-1/2 rotate-45 border-b border-r border-zinc-200 bg-white"
              />
            </motion.button>
          )}
        </AnimatePresence>
      </div>
    </div>
  );
}
