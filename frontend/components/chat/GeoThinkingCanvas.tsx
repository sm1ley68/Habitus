"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { motion, useReducedMotion } from "framer-motion";
import { useGoogleMap } from "@/lib/map/useGoogleMap";
import { removeAdvancedMarker, replaceDataLayer, toLatLng } from "@/lib/map/google";
import { CITY_CENTER } from "@/lib/map/constants";
import { useSession } from "@/lib/store/session";
import { STAGE_GLOW } from "@/lib/agent/stageVisuals";
import { DUR, EASE } from "@/lib/motion";
import type { Stage, LayerId } from "@/lib/agent/types";

const ACCENT = "#7C8CFF";
const POINT = "#5AB8E0";
const LINE = "#E0995A";
const CTX_LAYERS: LayerId[] = ["schools", "parks"];
const THINKING: Stage[] = ["linguistic", "geo", "context", "relaxation", "streaming"];

export function isThinking(stage: Stage) {
  return THINKING.includes(stage);
}

export default function GeoThinkingCanvas() {
  const stage = useSession((state) => state.stage);
  const city = useSession((state) => state.city);
  const layerData = useSession((state) => state.layerData);
  const loadLayer = useSession((state) => state.loadLayer);
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, unavailable } = useGoogleMap(container, { interactive: false, zoom: 12.7 });
  const reduce = useReducedMotion();
  const anchor = CITY_CENTER[city];

  const circleRef = useRef<google.maps.Circle | null>(null);
  const layerRef = useRef<google.maps.Data | null>(null);
  const anchorRef = useRef<google.maps.marker.AdvancedMarkerElement | null>(null);
  const breatheRef = useRef<number | null>(null);
  const [sweepKey, setSweepKey] = useState(0);

  useEffect(() => {
    CTX_LAYERS.forEach((id) => void loadLayer(id));
  }, [loadLayer]);

  const contextData = useMemo<GeoJSON.FeatureCollection>(() => ({
    type: "FeatureCollection",
    features: CTX_LAYERS.flatMap((id) => layerData[id]?.features ?? []),
  }), [layerData]);

  useEffect(() => {
    if (!map || !ready) return;
    map.setCenter(toLatLng(anchor));
    map.setZoom(12.7);

    const circle = new google.maps.Circle({
      map,
      center: toLatLng(anchor),
      radius: 900,
      fillColor: ACCENT,
      fillOpacity: 0,
      strokeColor: ACCENT,
      strokeOpacity: 0,
      strokeWeight: 1.6,
      clickable: false,
      zIndex: 2,
    });
    circleRef.current = circle;

    const layer = new google.maps.Data({ map });
    layerRef.current = layer;

    const element = document.createElement("div");
    element.className = "think-anchor";
    element.innerHTML = '<span class="think-anchor__ping"></span><span class="think-anchor__dot"></span>';
    anchorRef.current = new google.maps.marker.AdvancedMarkerElement({
      map,
      position: toLatLng(anchor),
      content: element,
      title: "Центр поиска",
    });

    return () => {
      if (breatheRef.current) clearInterval(breatheRef.current);
      removeAdvancedMarker(anchorRef.current);
      anchorRef.current = null;
      layer.setMap(null);
      layerRef.current = null;
      circle.setMap(null);
      circleRef.current = null;
    };
  }, [map, ready, anchor]);

  useEffect(() => {
    if (!layerRef.current) return;
    replaceDataLayer(layerRef.current, contextData);
  }, [contextData, ready]);

  useEffect(() => {
    const circle = circleRef.current;
    const layer = layerRef.current;
    if (!circle || !layer) return;
    if (breatheRef.current) clearInterval(breatheRef.current);

    const geoOn = ["geo", "context", "relaxation", "streaming"].includes(stage);
    const contextOn = ["context", "relaxation", "streaming"].includes(stage);
    const radius = stage === "relaxation" ? 1800 : 1350;
    circle.setOptions({
      fillOpacity: geoOn ? 0.1 : 0,
      strokeOpacity: geoOn ? 0.55 : 0,
      radius,
    });

    if (geoOn && !reduce) {
      let expanded = false;
      breatheRef.current = window.setInterval(() => {
        expanded = !expanded;
        circle.setRadius(expanded ? radius : radius * 0.82);
      }, 1500);
    }

    layer.setStyle((feature) => {
      const geometry = feature.getGeometry()?.getType();
      if (geometry === "Point" || geometry === "MultiPoint") {
        return {
          clickable: false,
          visible: contextOn,
          icon: {
            path: google.maps.SymbolPath.CIRCLE,
            fillColor: POINT,
            fillOpacity: 0.95,
            strokeColor: "#ffffff",
            strokeOpacity: 0.9,
            strokeWeight: 1.4,
            scale: 5,
          },
        };
      }
      return {
        clickable: false,
        visible: contextOn,
        strokeColor: LINE,
        strokeOpacity: 0.7,
        strokeWeight: 2,
        fillColor: LINE,
        fillOpacity: 0.1,
      };
    });

    if (stage === "context" && !reduce) setSweepKey((key) => key + 1);
    return () => {
      if (breatheRef.current) clearInterval(breatheRef.current);
    };
  }, [stage, reduce, ready]);

  if (unavailable) return <RadarFallback stage={stage} reduce={!!reduce} />;

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
      <StatusBar caption={STAGE_GLOW[stage].caption} color={STAGE_GLOW[stage].color} reduce={!!reduce} />
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

function RadarFallback({ stage, reduce }: { stage: Stage; reduce: boolean }) {
  const size = 300;
  const center = size / 2;
  const geoOn = ["geo", "context", "relaxation", "streaming"].includes(stage);
  const contextOn = ["context", "relaxation", "streaming"].includes(stage);
  const rings = [48, 82, stage === "relaxation" ? 128 : 116];
  const candidates = [[center + 60, center - 30], [center - 48, center + 40], [center + 20, center + 66], [center - 70, center - 24]];

  return (
    <div className="mx-auto w-full max-w-[540px] overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-[0_20px_44px_-28px_rgba(28,29,32,0.4)]">
      <div className="relative grid h-[300px] place-items-center bg-[#f6f7fb]">
        <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="h-full w-auto" aria-hidden="true">
          {[0.25, 0.5, 0.75].map((fraction) => (
            <g key={fraction} stroke="#e2e5ee" strokeWidth="1">
              <line x1={size * fraction} y1="0" x2={size * fraction} y2={size} />
              <line x1="0" y1={size * fraction} x2={size} y2={size * fraction} />
            </g>
          ))}
          {geoOn && rings.map((radius, index) => (
            <motion.circle
              key={radius}
              cx={center}
              cy={center}
              r={radius}
              fill="none"
              stroke={ACCENT}
              strokeWidth={index === 0 ? 1.8 : 1.1}
              strokeOpacity={0.5 - index * 0.13}
              initial={reduce ? false : { scale: 0.6, opacity: 0 }}
              animate={reduce ? undefined : { scale: [0.9, 1, 0.9], opacity: 0.5 }}
              transition={reduce ? undefined : { duration: 3, ease: EASE.glow, repeat: Infinity, delay: index * 0.3 }}
              style={{ transformOrigin: `${center}px ${center}px` }}
            />
          ))}
          {geoOn && <circle cx={center} cy={center} r={rings[2]} fill={ACCENT} fillOpacity="0.05" />}
          {geoOn && !reduce && (
            <motion.line
              x1={center}
              y1={center}
              x2={center}
              y2={center - rings[2]}
              stroke={ACCENT}
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeOpacity="0.7"
              animate={{ rotate: 360 }}
              transition={{ duration: 6, ease: "linear", repeat: Infinity }}
              style={{ transformOrigin: `${center}px ${center}px` }}
            />
          )}
          {contextOn && [[center + 40, center - 60], [center - 66, center + 10], [center + 74, center + 34]].map((point, index) => (
            <motion.circle
              key={index}
              cx={point[0]}
              cy={point[1]}
              r="4.5"
              fill={POINT}
              stroke="#fff"
              strokeWidth="1.4"
              initial={reduce ? false : { scale: 0, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              transition={{ duration: DUR.base, delay: index * 0.08 }}
              style={{ transformOrigin: `${point[0]}px ${point[1]}px` }}
            />
          ))}
          {stage === "streaming" && candidates.map((point, index) => (
            <motion.circle
              key={index}
              cx={point[0]}
              cy={point[1]}
              r="6"
              fill={ACCENT}
              initial={reduce ? false : { scale: 0, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              transition={{ duration: DUR.base, delay: index * 0.11 }}
              style={{ transformOrigin: `${point[0]}px ${point[1]}px` }}
            />
          ))}
          <circle cx={center} cy={center} r="5.5" fill={ACCENT} stroke="#fff" strokeWidth="1.6" />
          {!reduce && (
            <motion.circle
              cx={center}
              cy={center}
              r="5.5"
              fill="none"
              stroke={ACCENT}
              strokeWidth="1.4"
              animate={{ scale: [1, 2.6], opacity: [0.55, 0] }}
              transition={{ duration: 1.9, ease: EASE.glow, repeat: Infinity }}
              style={{ transformOrigin: `${center}px ${center}px` }}
            />
          )}
        </svg>
      </div>
      <StatusBar caption={STAGE_GLOW[stage].caption} color={STAGE_GLOW[stage].color} reduce={reduce} />
    </div>
  );
}
