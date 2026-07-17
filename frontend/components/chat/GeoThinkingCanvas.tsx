"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import maplibregl from "maplibre-gl";
import { motion, useReducedMotion } from "framer-motion";
import { useMaplibre } from "@/lib/map/useMaplibre";
import { CITY_CENTER } from "@/lib/map/constants";
import { useSession } from "@/lib/store/session";
import { STAGE_GLOW } from "@/lib/agent/stageVisuals";
import { DUR, EASE } from "@/lib/motion";
import type { Stage, LayerId } from "@/lib/agent/types";

// A live geo canvas for the "model is thinking" phase. It replays what the
// geo-spatial agent actually does — anchor the search, breathe a 15-minute
// reachability isochrone, draw the search zone, scan district layers, then pop
// the candidates — driven straight off the pipeline `stage`. Not a spinner: a
// small map that reasons. Falls back to an SVG radar when there's no map key.

const ACCENT = "#7C8CFF";
const POINT = "#5AB8E0";
const LINE = "#E0995A";

const EMPTY_FC: GeoJSON.FeatureCollection = { type: "FeatureCollection", features: [] };

const pt = (c: [number, number]): GeoJSON.Feature =>
  ({ type: "Feature", properties: {}, geometry: { type: "Point", coordinates: c } });

// Слои, которые «сканируются» на такте context. Берутся из GET /geo/layers —
// это настоящие школы и парки города, а не декорация. Пока слой не загрузился,
// коллекция пустая: канвас покажет пульс изохроны без точек, но не выдумает их.
const CTX_LAYERS: LayerId[] = ["schools", "parks"];

const THINKING: Stage[] = ["linguistic", "geo", "context", "relaxation", "streaming"];
export function isThinking(stage: Stage) {
  return THINKING.includes(stage);
}

export default function GeoThinkingCanvas() {
  const stage = useSession((s) => s.stage);
  const city = useSession((s) => s.city);
  const layerData = useSession((s) => s.layerData);
  const loadLayer = useSession((s) => s.loadLayer);
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, missingKey } = useMaplibre(container);
  const reduce = useReducedMotion();

  const anchor = CITY_CENTER[city];

  // Тянем реальные слои под такт context — один раз, дальше из кэша стора.
  useEffect(() => {
    CTX_LAYERS.forEach((id) => void loadLayer(id));
  }, [loadLayer]);

  const ctxFC = useMemo<GeoJSON.FeatureCollection>(() => ({
    type: "FeatureCollection",
    features: CTX_LAYERS.flatMap((id) => layerData[id]?.features ?? []),
  }), [layerData]);

  const breatheRef = useRef<number | null>(null);
  const anchorRef = useRef<maplibregl.Marker | null>(null);
  const [sweepKey, setSweepKey] = useState(0);

  // One-time source/layer + anchor setup.
  useEffect(() => {
    if (!map || !ready) return;
    map.scrollZoom.disable();
    map.doubleClickZoom.disable();
    map.dragRotate.disable();
    map.touchZoomRotate.disableRotation();
    map.jumpTo({ center: anchor, zoom: 12.7 });

    const add = () => {
      if (!map.getSource("anchor")) map.addSource("anchor", { type: "geojson", data: pt(anchor) });
      // Зоны на этом такте ещё нет: она приходит только в final_result и
      // рисуется на карте результатов. Рисовать её здесь — выдумывать.
      if (!map.getSource("ctx")) map.addSource("ctx", { type: "geojson", data: EMPTY_FC });

      if (!map.getLayer("reach-fill"))
        map.addLayer({ id: "reach-fill", type: "circle", source: "anchor",
          paint: { "circle-color": ACCENT, "circle-blur": 0.85, "circle-opacity": 0, "circle-radius": 24,
            "circle-opacity-transition": { duration: 600, delay: 0 }, "circle-radius-transition": { duration: 1500, delay: 0 } } });
      if (!map.getLayer("reach-ring"))
        map.addLayer({ id: "reach-ring", type: "circle", source: "anchor",
          paint: { "circle-color": "rgba(0,0,0,0)", "circle-stroke-color": ACCENT, "circle-stroke-width": 1.6,
            "circle-stroke-opacity": 0, "circle-radius": 24,
            "circle-stroke-opacity-transition": { duration: 600, delay: 0 }, "circle-radius-transition": { duration: 1500, delay: 0 } } });

      if (!map.getLayer("ctx-line"))
        map.addLayer({ id: "ctx-line", type: "line", source: "ctx",
          filter: ["==", ["geometry-type"], "LineString"],
          paint: { "line-color": LINE, "line-width": 2, "line-opacity": 0, "line-opacity-transition": { duration: 700, delay: 0 } } });
      if (!map.getLayer("ctx-point"))
        map.addLayer({ id: "ctx-point", type: "circle", source: "ctx",
          filter: ["==", ["geometry-type"], "Point"],
          paint: { "circle-color": POINT, "circle-radius": 5, "circle-stroke-color": "#fff", "circle-stroke-width": 1.4,
            "circle-opacity": 0, "circle-stroke-opacity": 0,
            "circle-opacity-transition": { duration: 700, delay: 0 }, "circle-stroke-opacity-transition": { duration: 700, delay: 0 } } });

      const el = document.createElement("div");
      el.className = "think-anchor";
      el.innerHTML = '<span class="think-anchor__ping"></span><span class="think-anchor__dot"></span>';
      anchorRef.current = new maplibregl.Marker({ element: el, anchor: "center" }).setLngLat(anchor).addTo(map);
    };
    add();

    return () => {
      if (breatheRef.current) clearInterval(breatheRef.current);
      anchorRef.current?.remove();
      try {
        ["reach-fill", "reach-ring", "ctx-line", "ctx-point"].forEach((id) => {
          if (map.getLayer(id)) map.removeLayer(id);
        });
        ["anchor", "ctx"].forEach((id) => { if (map.getSource(id)) map.removeSource(id); });
      } catch { /* map already gone */ }
    };
  }, [map, ready, anchor]);

  // Слои приезжают асинхронно — переливаем их в источник, когда пришли.
  useEffect(() => {
    if (!map || !ready) return;
    const src = map.getSource("ctx") as maplibregl.GeoJSONSource | undefined;
    src?.setData(ctxFC);
  }, [map, ready, ctxFC]);

  // Stage-driven choreography.
  useEffect(() => {
    if (!map || !ready) return;
    const set = (id: string, prop: string, val: unknown) => {
      if (map.getLayer(id)) map.setPaintProperty(id, prop, val as never);
    };
    if (breatheRef.current) clearInterval(breatheRef.current);

    const geoOn = stage === "geo" || stage === "context" || stage === "relaxation" || stage === "streaming";
    const ctxOn = stage === "context" || stage === "relaxation" || stage === "streaming";

    // Reachability isochrone — breathes while the geo agent works; widens on relaxation.
    if (geoOn) {
      const baseR = stage === "relaxation" ? 122 : 94;
      set("reach-fill", "circle-opacity", 0.14);
      set("reach-ring", "circle-stroke-opacity", 0.55);
      if (reduce) {
        set("reach-fill", "circle-radius", baseR);
        set("reach-ring", "circle-radius", baseR);
      } else {
        let big = false;
        const pulse = () => {
          big = !big;
          const r = big ? baseR : baseR * 0.8;
          set("reach-fill", "circle-radius", r);
          set("reach-ring", "circle-radius", r);
        };
        pulse();
        breatheRef.current = window.setInterval(pulse, 1500);
      }
    } else {
      set("reach-fill", "circle-opacity", 0);
      set("reach-ring", "circle-stroke-opacity", 0);
    }

    // District layers scanned during context.
    set("ctx-line", "line-opacity", ctxOn ? 0.7 : 0);
    set("ctx-point", "circle-opacity", ctxOn ? 0.95 : 0);
    set("ctx-point", "circle-stroke-opacity", ctxOn ? 0.9 : 0);

    // A single scan sweep as the context beat begins.
    if (stage === "context" && !reduce) setSweepKey((k) => k + 1);

    // Кандидатов на этом такте ещё не существует — они приходят в final_result
    // вместе со стадией done. Показывать пины «заранее» значило бы рисовать
    // выдуманные объекты, поэтому канвас их просто не рисует.

    return () => { if (breatheRef.current) clearInterval(breatheRef.current); };
  }, [map, ready, stage, reduce]);

  if (missingKey) return <RadarFallback stage={stage} reduce={!!reduce} />;

  const caption = STAGE_GLOW[stage].caption;
  return (
    <motion.div
      initial={reduce ? false : { opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: DUR.slow, ease: EASE.emphasizedDecelerate }}
      className="mx-auto w-full max-w-[540px] overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-[0_20px_44px_-28px_rgba(28,29,32,0.4)]"
    >
      <div className="relative h-[300px] w-full bg-[#f6f7fb]">
        <div ref={container} className="absolute inset-0" />
        <div aria-hidden className="pointer-events-none absolute inset-0 shadow-[inset_0_0_0_1px_rgba(20,20,34,0.05)]" />
        {sweepKey > 0 && !reduce && (
          <motion.div
            key={sweepKey}
            aria-hidden
            className="pointer-events-none absolute inset-y-0 w-1/3 mix-blend-multiply"
            style={{ background: "linear-gradient(100deg, transparent, rgba(124,140,255,0.16) 48%, rgba(124,140,255,0.05) 60%, transparent)" }}
            initial={{ x: "-140%" }}
            animate={{ x: "320%" }}
            transition={{ duration: 1.5, ease: EASE.standard }}
          />
        )}
      </div>
      <StatusBar caption={caption} color={STAGE_GLOW[stage].color} reduce={!!reduce} />
    </motion.div>
  );
}

function StatusBar({ caption, color, reduce }: { caption: string; color: string; reduce: boolean }) {
  return (
    <div className="flex items-center gap-2.5 border-t border-zinc-100 px-4 py-2.5 text-sm text-zinc-600">
      <span className="relative flex h-2 w-2 shrink-0">
        {!reduce && (
          <motion.span
            className="absolute inline-flex h-full w-full rounded-full"
            style={{ background: color }}
            animate={{ opacity: [0.6, 0, 0.6], scale: [1, 2.2, 1] }}
            transition={{ duration: 1.8, ease: EASE.glow, repeat: Infinity }}
          />
        )}
        <span className="relative inline-flex h-2 w-2 rounded-full" style={{ background: color }} />
      </span>
      <span className="font-mono text-xs uppercase tracking-[0.14em] text-zinc-400">geo-engine</span>
      <span className="ml-auto text-right">{caption || "Готовлю ответ…"}</span>
    </div>
  );
}

// SVG radar shown when there's no map key — concentric reachability rings, a
// rotating scan line, the anchor, and candidates that land on the "streaming"
// beat. Same narrative, zero dependencies.
function RadarFallback({ stage, reduce }: { stage: Stage; reduce: boolean }) {
  const S = 300, C = S / 2;
  const geoOn = stage === "geo" || stage === "context" || stage === "relaxation" || stage === "streaming";
  const ctxOn = stage === "context" || stage === "relaxation" || stage === "streaming";
  const rings = [48, 82, stage === "relaxation" ? 128 : 116];
  const cand = [[C + 60, C - 30], [C - 48, C + 40], [C + 20, C + 66], [C - 70, C - 24]];

  return (
    <div className="mx-auto w-full max-w-[540px] overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-[0_20px_44px_-28px_rgba(28,29,32,0.4)]">
      <div className="relative grid h-[300px] place-items-center bg-[#f6f7fb]">
        <svg width={S} height={S} viewBox={`0 0 ${S} ${S}`} className="h-full w-auto" aria-hidden="true">
          {/* faint grid */}
          {[0.25, 0.5, 0.75].map((f) => (
            <g key={f} stroke="#e2e5ee" strokeWidth="1">
              <line x1={S * f} y1="0" x2={S * f} y2={S} />
              <line x1="0" y1={S * f} x2={S} y2={S * f} />
            </g>
          ))}
          {/* reachability rings */}
          {geoOn && rings.map((r, i) => (
            <motion.circle
              key={i} cx={C} cy={C} r={r} fill="none" stroke={ACCENT}
              strokeWidth={i === 0 ? 1.8 : 1.1} strokeOpacity={0.5 - i * 0.13}
              initial={reduce ? false : { scale: 0.6, opacity: 0 }}
              animate={reduce ? undefined : { scale: [0.9, 1, 0.9], opacity: [0.5, 0.5, 0.5] }}
              transition={reduce ? undefined : { duration: 3, ease: EASE.glow, repeat: Infinity, delay: i * 0.3 }}
              style={{ transformOrigin: `${C}px ${C}px` }}
            />
          ))}
          {geoOn && <circle cx={C} cy={C} r={rings[2]} fill={ACCENT} fillOpacity="0.05" />}
          {/* rotating scan line */}
          {geoOn && !reduce && (
            <motion.line
              x1={C} y1={C} x2={C} y2={C - rings[2]} stroke={ACCENT} strokeWidth="1.6" strokeLinecap="round" strokeOpacity="0.7"
              animate={{ rotate: 360 }} transition={{ duration: 6, ease: "linear", repeat: Infinity }}
              style={{ transformOrigin: `${C}px ${C}px` }}
            />
          )}
          {/* district points during context */}
          {ctxOn && [[C + 40, C - 60], [C - 66, C + 10], [C + 74, C + 34]].map((p, i) => (
            <motion.circle key={i} cx={p[0]} cy={p[1]} r="4.5" fill={POINT} stroke="#fff" strokeWidth="1.4"
              initial={reduce ? false : { scale: 0, opacity: 0 }} animate={{ scale: 1, opacity: 1 }}
              transition={{ duration: DUR.base, delay: i * 0.08 }} style={{ transformOrigin: `${p[0]}px ${p[1]}px` }} />
          ))}
          {/* candidates on streaming */}
          {stage === "streaming" && cand.map((p, i) => (
            <motion.circle key={i} cx={p[0]} cy={p[1]} r="6" fill={ACCENT}
              initial={reduce ? false : { scale: 0, opacity: 0 }} animate={{ scale: 1, opacity: 1 }}
              transition={{ duration: DUR.base, delay: i * 0.11 }} style={{ transformOrigin: `${p[0]}px ${p[1]}px` }} />
          ))}
          {/* anchor */}
          <circle cx={C} cy={C} r="5.5" fill={ACCENT} stroke="#fff" strokeWidth="1.6" />
          {!reduce && (
            <motion.circle cx={C} cy={C} r="5.5" fill="none" stroke={ACCENT} strokeWidth="1.4"
              animate={{ scale: [1, 2.6], opacity: [0.55, 0] }} transition={{ duration: 1.9, ease: EASE.glow, repeat: Infinity }}
              style={{ transformOrigin: `${C}px ${C}px` }} />
          )}
        </svg>
      </div>
      <StatusBar caption={STAGE_GLOW[stage].caption} color={STAGE_GLOW[stage].color} reduce={reduce} />
    </div>
  );
}
