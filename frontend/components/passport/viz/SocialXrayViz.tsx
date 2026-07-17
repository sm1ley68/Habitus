"use client";
import { useEffect, useRef, useState } from "react";
import { motion, useInView, useReducedMotion } from "framer-motion";
import maplibregl from "maplibre-gl";
import { useMaplibre } from "@/lib/map/useMaplibre";
import { DUR, EASE } from "@/lib/motion";
import type { SocialEnvironmentData, SocialLayerId } from "@/lib/agent/types";
import type { VizProps } from "./index";

const ACCENT = "#7C8CFF";

// Each social layer gets its own desaturated tint so heat stays legible on the
// neutral canvas — never neon, never the loud accent (that's reserved for the
// home ring). communal = muted slate, bars = muted amber, crime = deep rose.
const LAYER_TINT: Record<SocialLayerId, string> = {
  communal: "#8b93bb",
  bars: "#c0894f",
  crime: "#b4636f",
};

// The three meters read low→high on a single desaturated emerald→rose ramp.
// Lower is always better, so the ramp doubles as a "good→bad" cue — but the
// word label below each meter carries the same signal (colour is never alone).
const GOOD = "#6f9e79"; // desaturated emerald
const BAD = "#b4636f"; // deep rose

const LAYERS: { id: SocialLayerId; short: string }[] = [
  { id: "communal", short: "Коммуналки" },
  { id: "bars", short: "Бары" },
  { id: "crime", short: "Криминал" },
];

const METERS: { key: keyof SocialEnvironmentData["scores"]; label: string }[] = [
  { key: "communal_share", label: "Доля коммуналок" },
  { key: "bars_density", label: "Плотность баров" },
  { key: "crime_index", label: "Крим-индекс" },
];

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

// hex lerp for the meter fill: good (emerald) at 0, bad (rose) at 1.
function hx(n: number) {
  return clamp(Math.round(n), 0, 255).toString(16).padStart(2, "0");
}
function mix(a: string, b: string, t: number) {
  const pa = [1, 3, 5].map((i) => parseInt(a.slice(i, i + 2), 16));
  const pb = [1, 3, 5].map((i) => parseInt(b.slice(i, i + 2), 16));
  return `#${hx(pa[0] + (pb[0] - pa[0]) * t)}${hx(pa[1] + (pb[1] - pa[1]) * t)}${hx(pa[2] + (pb[2] - pa[2]) * t)}`;
}

function riskWord(v: number) {
  if (v < 0.34) return "низкий";
  if (v < 0.67) return "умеренный";
  return "высокий";
}

type LngLat = [number, number];

// A ground-truth radius ring: metres → a lng/lat polygon around home. The
// cos(lat) correction keeps it round at Petersburg latitudes, not an ellipse.
function ringPolygon(center: LngLat, radiusM: number, steps = 72): LngLat[] {
  const dLat = radiusM / 111_320;
  const dLng = radiusM / (111_320 * Math.cos((center[1] * Math.PI) / 180));
  const out: LngLat[] = [];
  for (let i = 0; i <= steps; i++) {
    const a = (i / steps) * Math.PI * 2;
    out.push([center[0] + dLng * Math.cos(a), center[1] + dLat * Math.sin(a)]);
  }
  return out;
}

function fillId(id: SocialLayerId) {
  return `xray-fill-${id}`;
}
function circleId(id: SocialLayerId) {
  return `xray-circle-${id}`;
}

export default function SocialXrayViz({ data, home: objectHome }: VizProps) {
  const social = data as SocialEnvironmentData | undefined;
  const reduce = useReducedMotion();

  const rootRef = useRef<HTMLDivElement>(null);
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, missingKey } = useMaplibre(container);

  // Revealed once the panel scrolls into view — drives both the sweep and the
  // heat fade-in. `once` so it plays a single time, never on every scroll.
  const inView = useInView(rootRef, { once: true, margin: "-60px" });
  const revealed = inView;

  const [active, setActive] = useState<SocialLayerId>("communal");

  const scores = social?.scores ?? { communal_share: 0, bars_density: 0, crime_index: 0 };

  // --- Map: home ring + heat layers, built once GL is ready. ---
  useEffect(() => {
    if (!map || !ready || !social) return;
    // home в payload опционален (контракт §2.2). Запасной вариант — координаты
    // самого объекта: кольцо «дома» рисуется вокруг настоящего дома, а не
    // вокруг выдуманной точки. Нет ни того, ни другого — карту не рисуем.
    const home = social.home ?? objectHome;
    if (!home) return;
    const reduced = reduce ?? false;

    map.scrollZoom.disable();
    map.doubleClickZoom.disable();
    map.dragRotate.disable();
    map.touchZoomRotate.disableRotation();

    const ring = ringPolygon(home, social.radius_m);
    const markers: maplibregl.Marker[] = [];
    const ownedLayers: string[] = [];
    const ownedSources: string[] = [];

    // Frame the whole ring with a little breathing room.
    const bounds = ring.reduce(
      (b, c) => b.extend(c),
      new maplibregl.LngLatBounds(home, home),
    );
    map.fitBounds(bounds, {
      padding: 34,
      duration: reduced ? 0 : DUR.slow * 1000,
      maxZoom: 15.5,
    });

    // Home ring — the single accent-coloured mark on the canvas.
    if (!map.getSource("xray-ring")) {
      map.addSource("xray-ring", {
        type: "geojson",
        data: { type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [ring] } },
      });
      ownedSources.push("xray-ring");
      map.addLayer({
        id: "xray-ring-fill",
        type: "fill",
        source: "xray-ring",
        paint: { "fill-color": ACCENT, "fill-opacity": 0.05 },
      });
      map.addLayer({
        id: "xray-ring-line",
        type: "line",
        source: "xray-ring",
        layout: { "line-cap": "round", "line-join": "round" },
        paint: { "line-color": ACCENT, "line-width": 1.4, "line-opacity": 0.7, "line-dasharray": [2, 2] },
      });
      ownedLayers.push("xray-ring-fill", "xray-ring-line");
    }

    // Heat features, keyed by properties.layer. One fill layer (polygons) and one
    // circle layer (points) per social layer so we can crossfade by opacity.
    if (!map.getSource("xray-heat")) {
      map.addSource("xray-heat", { type: "geojson", data: social.heat });
      ownedSources.push("xray-heat");
      const fadeMs = reduced ? 0 : DUR.slow * 1000;
      for (const { id } of LAYERS) {
        const tint = LAYER_TINT[id];
        map.addLayer({
          id: fillId(id),
          type: "fill",
          source: "xray-heat",
          filter: ["all", ["==", ["get", "layer"], id], ["==", ["geometry-type"], "Polygon"]],
          paint: {
            "fill-color": tint,
            "fill-opacity": 0,
            "fill-opacity-transition": { duration: fadeMs, delay: 0 },
            "fill-outline-color": tint,
          },
        });
        map.addLayer({
          id: circleId(id),
          type: "circle",
          source: "xray-heat",
          filter: ["all", ["==", ["get", "layer"], id], ["==", ["geometry-type"], "Point"]],
          paint: {
            "circle-color": tint,
            "circle-radius": 9,
            "circle-blur": 0.55,
            "circle-opacity": 0,
            "circle-opacity-transition": { duration: fadeMs, delay: 0 },
            "circle-stroke-color": tint,
            "circle-stroke-width": 1,
            "circle-stroke-opacity": 0,
            "circle-stroke-opacity-transition": { duration: fadeMs, delay: 0 },
          },
        });
        ownedLayers.push(fillId(id), circleId(id));
      }
    }

    // Home dot — a calm anchor at the centre of the ring.
    const homeEl = document.createElement("div");
    homeEl.style.cssText =
      "width:12px;height:12px;border-radius:9999px;background:#fff;" +
      `box-shadow:0 0 0 2px ${ACCENT},0 1px 4px rgba(20,20,34,0.25);`;
    markers.push(new maplibregl.Marker({ element: homeEl, anchor: "center" }).setLngLat(home).addTo(map));

    return () => {
      markers.forEach((m) => m.remove());
      try {
        ownedLayers.forEach((id) => { if (map.getLayer(id)) map.removeLayer(id); });
        ownedSources.forEach((id) => { if (map.getSource(id)) map.removeSource(id); });
      } catch { /* map already torn down */ }
    };
    // Досье объекта неизменно на время показа карточки; пересобираем слои
    // только когда готов GL-стиль или сменился сам объект.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, ready, social, objectHome]);

  // --- Crossfade: only the active layer is visible, and only once revealed. ---
  useEffect(() => {
    if (!map || !ready || !social) return;
    for (const { id } of LAYERS) {
      const on = revealed && id === active;
      try {
        if (map.getLayer(fillId(id))) map.setPaintProperty(fillId(id), "fill-opacity", on ? 0.34 : 0);
        if (map.getLayer(circleId(id))) {
          map.setPaintProperty(circleId(id), "circle-opacity", on ? 0.42 : 0);
          map.setPaintProperty(circleId(id), "circle-stroke-opacity", on ? 0.7 : 0);
        }
      } catch { /* layers not ready yet */ }
    }
  }, [map, ready, social, active, revealed]);

  return (
    <div
      ref={rootRef}
      data-testid="social-xray"
      className="grid gap-4 lg:grid-cols-[1fr_15rem]"
    >
      {/* Map column — optional. The scores panel below stands on its own. */}
      {!missingKey && (
        <div className="relative overflow-hidden rounded-2xl bg-[#f6f7fb] ring-1 ring-inset ring-black/[0.06]">
          <div className="relative h-64 w-full lg:h-full lg:min-h-[16rem]">
            <div ref={container} className="absolute inset-0" />
            {/* Scanning sweep — an accent glow crossing the map as heat fades in
                behind it. Purely decorative, so pointer-events-none. */}
            {!reduce && (
              <motion.div
                aria-hidden
                className="pointer-events-none absolute inset-y-0 left-0 w-1/3"
                style={{ background: "linear-gradient(90deg, transparent, rgba(124,140,255,0.34), transparent)" }}
                initial={{ x: "-110%", opacity: 0 }}
                whileInView={{ x: "310%", opacity: [0, 1, 1, 0] }}
                viewport={{ once: true, margin: "-60px" }}
                transition={{ duration: DUR.cinematic, ease: EASE.standard, delay: 0.15 }}
              />
            )}
            <div
              aria-hidden
              className="pointer-events-none absolute inset-0 shadow-[inset_0_0_0_1px_rgba(20,20,34,0.05)]"
            />
          </div>
        </div>
      )}

      {/* Scores + controls — always rendered, map or no map. */}
      <div className="flex flex-col gap-4">
        {/* Segmented control toggles which heat layer the map shows. */}
        <div
          role="group"
          aria-label="Слой на карте"
          className="grid grid-cols-3 gap-1 rounded-xl bg-zinc-100 p-1 ring-1 ring-inset ring-black/[0.05]"
        >
          {LAYERS.map(({ id, short }) => {
            const on = active === id;
            return (
              <button
                key={id}
                type="button"
                aria-pressed={on}
                onClick={() => setActive(id)}
                className={`rounded-lg px-2 py-1.5 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
                  on ? "bg-white text-zinc-800 shadow-sm ring-1 ring-inset ring-black/[0.06]" : "text-zinc-500 hover:text-zinc-700"
                }`}
              >
                <span className="flex items-center justify-center gap-1.5">
                  <span
                    aria-hidden
                    className="h-2 w-2 rounded-full"
                    style={{ background: LAYER_TINT[id], opacity: on ? 1 : 0.55 }}
                  />
                  {short}
                </span>
              </button>
            );
          })}
        </div>

        <div className="flex flex-col gap-3.5">
          {METERS.map(({ key, label }, i) => {
            const v = clamp(Number(scores[key] ?? 0), 0, 1);
            const color = mix(GOOD, BAD, v);
            return (
              <div key={key}>
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-xs font-medium text-zinc-600">{label}</span>
                  <span className="flex items-baseline gap-1.5">
                    <span className="text-[11px] uppercase tracking-wide text-zinc-400">{riskWord(v)}</span>
                    <span className="font-mono text-xs tabular-nums text-zinc-700">{v.toFixed(2)}</span>
                  </span>
                </div>
                <div
                  role="meter"
                  aria-label={`${label}: риск ${riskWord(v)}`}
                  aria-valuemin={0}
                  aria-valuemax={1}
                  aria-valuenow={v}
                  className="mt-1.5 h-2 w-full overflow-hidden rounded-full bg-zinc-100 ring-1 ring-inset ring-black/[0.05]"
                >
                  <motion.div
                    className="h-full w-full origin-left rounded-full"
                    style={{ background: color, transformOrigin: "left center" }}
                    initial={reduce ? false : { scaleX: 0 }}
                    whileInView={{ scaleX: v }}
                    viewport={{ once: true, margin: "-40px" }}
                    transition={{ duration: DUR.slow, ease: EASE.standard, delay: 0.1 + i * 0.08 }}
                  />
                </div>
              </div>
            );
          })}
          <p className="text-[11px] leading-snug text-zinc-400">
            Шкала: ниже — лучше. Цвет идёт от спокойного зелёного к глубокому розовому.
          </p>
        </div>
      </div>
    </div>
  );
}
