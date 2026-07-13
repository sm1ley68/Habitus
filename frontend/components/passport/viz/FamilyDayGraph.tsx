"use client";
import { useEffect, useRef, useState } from "react";
import maplibregl from "maplibre-gl";
import { motion, useReducedMotion } from "framer-motion";
import { useMaplibre } from "@/lib/map/useMaplibre";
import { DUR, EASE } from "@/lib/motion";
import type { FamilyRoutingData, TravelMode } from "@/lib/agent/types";
import type { VizProps } from "./index";

// The one saturated brand color — the home anchor only.
const ACCENT = "#7C8CFF";
// One desaturated tint per household member (max 3), so the three journeys stay
// legible against each other without any of them screaming.
const MEMBER_TINTS = ["#7C8CFF", "#6f9e79", "#8b93bb"] as const;

// The Gantt fill encodes the travel MODE (member identity comes from the lane).
// Muted mid-tones — the tint hints at the mode, the text label states it.
const MODE: Record<TravelMode, { label: string; color: string }> = {
  walk: { label: "пешком", color: "#6f9e79" },
  scooter: { label: "самокат", color: "#c98a52" },
  bus: { label: "автобус", color: "#c9a94e" },
  car: { label: "машина", color: "#8b93bb" },
  metro: { label: "метро", color: "#6f9bc0" },
};
const CAUTION = "#F5A623";

const HOME_ICON =
  '<path d="M2 6 L6 2.5 L10 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/><path d="M3.2 5.5 V10 H8.8 V5.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>';

// Day window for the whole viz: an early school run through a late return.
const DAY_START = 6;
const DAY_END = 23;

const BASE_W = 3;
const HI_W = 5.5;

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));
const easeOutCubic = (t: number) => 1 - Math.pow(1 - t, 3);
const pad2 = (n: number) => String(n).padStart(2, "0");

function hoursOf(t: string): number {
  const [h, m] = t.split(":").map(Number);
  return h + (m || 0) / 60;
}
function fmtTime(hour: number): string {
  const h = Math.floor(hour);
  const m = Math.round((hour - h) * 60);
  return `${pad2(h)}:${pad2(m === 60 ? 0 : m)}`;
}
// 0..100 position on the day scale.
const xPct = (hour: number) => clamp((hour - DAY_START) / (DAY_END - DAY_START), 0, 1) * 100;

type LngLat = [number, number];

// Equirectangular metre distance — accurate enough over these ~1 km commutes and
// cheap per frame.
function segMetres(a: LngLat, b: LngLat): number {
  const R = 6371000;
  const lat = ((a[1] + b[1]) / 2) * (Math.PI / 180);
  const dLat = (b[1] - a[1]) * (Math.PI / 180);
  const dLng = (b[0] - a[0]) * (Math.PI / 180) * Math.cos(lat);
  return R * Math.hypot(dLat, dLng);
}

// Precompute cumulative length so a line can reveal by distance-fraction and place
// a traveller exactly on the polyline (not on a chord).
function prepare(coords: LngLat[]) {
  const cum = [0];
  for (let i = 1; i < coords.length; i++) cum.push(cum[i - 1] + segMetres(coords[i - 1], coords[i]));
  return { coords, cum, total: cum[cum.length - 1] || 1 };
}
type Prepared = ReturnType<typeof prepare>;

function sliceAt(p: Prepared, t: number): { line: LngLat[]; head: LngLat } {
  if (t >= 1) return { line: p.coords, head: p.coords[p.coords.length - 1] };
  const target = p.total * t;
  const line: LngLat[] = [p.coords[0]];
  for (let i = 1; i < p.coords.length; i++) {
    if (p.cum[i] < target) { line.push(p.coords[i]); continue; }
    const seg = p.cum[i] - p.cum[i - 1];
    const f = seg > 0 ? (target - p.cum[i - 1]) / seg : 0;
    const a = p.coords[i - 1], b = p.coords[i];
    const head: LngLat = [a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f];
    line.push(head);
    return { line, head };
  }
  return { line: p.coords, head: p.coords[p.coords.length - 1] };
}

function lineFeature(coords: LngLat[]): GeoJSON.Feature {
  return { type: "Feature", properties: {}, geometry: { type: "LineString", coordinates: coords } };
}

// Stitch a member's legs into one continuous route, dropping the duplicated
// junction point where one leg's end meets the next leg's start.
function concatLegs(legs: FamilyRoutingData["members"][number]["legs"]): LngLat[] {
  const out: LngLat[] = [];
  legs.forEach((leg, i) => {
    (leg.geometry.coordinates as LngLat[]).forEach((pt, j) => {
      if (i > 0 && j === 0) {
        const last = out[out.length - 1];
        if (last && last[0] === pt[0] && last[1] === pt[1]) return;
      }
      out.push(pt);
    });
  });
  return out;
}

type LegRun = { prep: Prepared; departH: number; arriveH: number };
type MemberRun = { id: string; tint: string; legs: LegRun[]; marker: maplibregl.Marker | null };

// Where a member is at time `hour`: interpolated along the active leg, parked at a
// leg's end during any wait, home before departure, destination after arrival.
function posAt(m: MemberRun, hour: number): LngLat {
  const legs = m.legs;
  if (!legs.length) return [0, 0];
  const first = legs[0];
  const last = legs[legs.length - 1];
  const endOf = (l: LegRun) => l.prep.coords[l.prep.coords.length - 1];
  if (hour <= first.departH) return first.prep.coords[0];
  if (hour >= last.arriveH) return endOf(last);
  for (let i = 0; i < legs.length; i++) {
    const L = legs[i];
    if (hour <= L.arriveH) {
      if (hour >= L.departH) {
        const f = (hour - L.departH) / Math.max(1e-6, L.arriveH - L.departH);
        return sliceAt(L.prep, f).head;
      }
      return endOf(legs[i - 1]); // waiting between legs
    }
  }
  return endOf(last);
}

const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export default function FamilyDayGraph({ data }: VizProps) {
  const reduce = useReducedMotion();
  const routing = data as FamilyRoutingData | undefined;
  const members = (routing?.members ?? []).slice(0, MEMBER_TINTS.length);

  const container = useRef<HTMLDivElement>(null);
  const { map, ready, missingKey } = useMaplibre(container);

  // Settled frame: everyone has already arrived (dots at journey end).
  const [hour, setHour] = useState(DAY_END);
  const [playing, setPlaying] = useState(false);
  const [active, setActive] = useState<string | null>(null);

  const hourRef = useRef(hour);
  hourRef.current = hour;
  const runtimeRef = useRef<MemberRun[]>([]);
  const layerRef = useRef<Map<string, string>>(new Map());
  const raf = useRef<number | null>(null);
  const last = useRef(0);

  // --- Map: home anchor, self-revealing per-member routes, traveller dots. ---
  useEffect(() => {
    if (!map || !ready || !routing) return;
    const reduced = prefersReducedMotion();
    const tints = members.map((_, i) => MEMBER_TINTS[i]);

    // Calm viz: page scroll wins; a gentle pan is still allowed.
    map.scrollZoom.disable();
    map.doubleClickZoom.disable();
    map.dragRotate.disable();
    map.touchZoomRotate.disableRotation();

    let cancelled = false;
    const rafs: number[] = [];
    const markers: maplibregl.Marker[] = [];
    const layerIds: string[] = [];
    const sourceIds: string[] = [];
    layerRef.current = new Map();

    const runtime: MemberRun[] = members.map((m, i) => ({
      id: m.id,
      tint: tints[i],
      legs: m.legs.map((leg) => ({
        prep: prepare(leg.geometry.coordinates as LngLat[]),
        departH: hoursOf(leg.depart),
        arriveH: hoursOf(leg.arrive),
      })),
      marker: null,
    }));
    runtimeRef.current = runtime;

    // Frame the door plus every point of every route.
    const bounds = new maplibregl.LngLatBounds(routing.home, routing.home);
    bounds.extend(routing.home);
    members.forEach((m) => m.legs.forEach((leg) =>
      (leg.geometry.coordinates as LngLat[]).forEach((c) => bounds.extend(c))));
    map.fitBounds(bounds, {
      padding: { top: 44, bottom: 44, left: 44, right: 44 },
      duration: reduced ? 0 : DUR.slow * 1000,
      maxZoom: 15,
    });

    // The door — the shared start of the whole day.
    const homeEl = document.createElement("div");
    homeEl.className = "lmap-pin lmap-pin--home";
    homeEl.style.setProperty("--tint", ACCENT);
    homeEl.innerHTML =
      `<span class="lmap-pin__dot"><svg viewBox="0 0 12 12" aria-hidden="true">${HOME_ICON}</svg></span>` +
      `<span class="lmap-pin__label">Дом</span>`;
    markers.push(new maplibregl.Marker({ element: homeEl, anchor: "center" }).setLngLat(routing.home).addTo(map));

    const dur = DUR.cinematic * 1000;
    runtime.forEach((run, i) => {
      const member = members[i];
      const coords = concatLegs(member.legs);
      if (coords.length < 2) return;
      const prepped = prepare(coords);
      const sourceId = `fam-src-${i}`;
      const layerId = `fam-line-${i}`;

      map.addSource(sourceId, { type: "geojson", data: lineFeature([coords[0]]) });
      map.addLayer({
        id: layerId,
        type: "line",
        source: sourceId,
        layout: { "line-cap": "round", "line-join": "round" },
        paint: { "line-color": run.tint, "line-width": BASE_W, "line-blur": 0.3, "line-opacity": 0.85 },
      });
      sourceIds.push(sourceId);
      layerIds.push(layerId);
      layerRef.current.set(run.id, layerId);
      const source = map.getSource(sourceId) as maplibregl.GeoJSONSource;

      if (reduced) {
        source.setData(lineFeature(coords));
      } else {
        const delay = i * 220 + 180;
        const start = performance.now();
        const step = (now: number) => {
          if (cancelled) return;
          const t = clamp((now - start - delay) / dur, 0, 1);
          const { line } = sliceAt(prepped, easeOutCubic(t));
          source.setData(lineFeature(line));
          if (t < 1) rafs.push(requestAnimationFrame(step));
        };
        rafs.push(requestAnimationFrame(step));
      }

      // Traveller dot, positioned by the scrubbed hour (updated in a separate effect).
      const dotEl = document.createElement("div");
      dotEl.className = "lmap-traveller";
      dotEl.style.background = run.tint;
      dotEl.style.boxShadow = `0 0 0 4px ${run.tint}33, 0 1px 4px rgba(20,20,34,0.3)`;
      const marker = new maplibregl.Marker({ element: dotEl, anchor: "center" })
        .setLngLat(posAt(run, hourRef.current))
        .addTo(map);
      run.marker = marker;
      markers.push(marker);
    });

    return () => {
      cancelled = true;
      rafs.forEach(cancelAnimationFrame);
      markers.forEach((m) => m.remove());
      runtimeRef.current = [];
      layerRef.current = new Map();
      try {
        layerIds.forEach((id) => { if (map.getLayer(id)) map.removeLayer(id); });
        sourceIds.forEach((id) => { if (map.getSource(id)) map.removeSource(id); });
      } catch { /* map already torn down */ }
    };
    // members/routing are stable mock data; run once the GL style is ready.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, ready]);

  // Move every traveller dot to the scrubbed time.
  useEffect(() => {
    if (!ready) return;
    runtimeRef.current.forEach((m) => { if (m.marker) m.marker.setLngLat(posAt(m, hour)); });
  }, [hour, ready]);

  // Hover/focus on a lane segment widens & brightens that member's route.
  useEffect(() => {
    if (!map || !ready) return;
    layerRef.current.forEach((layerId, id) => {
      if (!map.getLayer(layerId)) return;
      const on = id === active;
      map.setPaintProperty(layerId, "line-width", active !== null && on ? HI_W : BASE_W);
      map.setPaintProperty(layerId, "line-opacity", active === null ? 0.85 : on ? 1 : 0.3);
    });
  }, [active, map, ready]);

  // Autoplay: one sweep across the day, then idle (never loops).
  useEffect(() => {
    if (!playing || reduce) return;
    const tick = (t: number) => {
      if (!last.current) last.current = t;
      const dt = (t - last.current) / 1000;
      last.current = t;
      let done = false;
      setHour((h) => {
        const next = h + dt * 3.4; // ~5s to cross the day
        if (next >= DAY_END) { done = true; return DAY_END; }
        return next;
      });
      if (done) { setPlaying(false); return; }
      raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
    return () => {
      if (raf.current) cancelAnimationFrame(raf.current);
      last.current = 0;
    };
  }, [playing, reduce]);

  if (!routing || members.length === 0) {
    return (
      <div className="grid h-40 w-full place-items-center rounded-2xl bg-[#f6f7fb] px-6 text-center text-sm text-zinc-400 ring-1 ring-inset ring-black/[0.05]">
        Нет данных о маршрутах семьи
      </div>
    );
  }

  const ticks = [6, 9, 12, 15, 18, 21, 23];
  const usedModes = Array.from(new Set(members.flatMap((m) => m.legs.map((l) => l.mode))));
  const hasCaution = members.some((m) => m.legs.some((l) => l.safety === "caution"));

  const startPlay = () => {
    if (hour >= DAY_END - 0.01) setHour(DAY_START);
    setPlaying(true);
  };

  return (
    <div data-testid="family-day-graph" className="overflow-hidden rounded-2xl ring-1 ring-inset ring-black/[0.06]">
      {/* Live map — hidden gracefully when no MapTiler key is present. */}
      {missingKey ? (
        <div className="grid h-56 w-full place-items-center bg-[#f6f7fb] px-6 text-center text-sm text-zinc-400">
          Карта маршрутов появится с ключом MapTiler
        </div>
      ) : (
        <div className="relative h-56 w-full bg-[#f6f7fb]">
          <div ref={container} className="absolute inset-0" />
          <div aria-hidden className="pointer-events-none absolute inset-0 shadow-[inset_0_0_0_1px_rgba(20,20,34,0.05)]" />
        </div>
      )}

      {/* Gantt day-lane band — plain HTML, independent of the map. */}
      <div className="border-t border-zinc-100 bg-white px-3.5 pt-3 pb-2">
        {/* Time axis */}
        <div className="relative ml-[5.5rem] mb-1.5 h-4 text-[10px] text-zinc-400">
          {ticks.map((t) => (
            <span
              key={t}
              className="absolute top-0 -translate-x-1/2 font-mono tabular-nums"
              style={{ left: `${xPct(t)}%` }}
            >
              {pad2(t)}
            </span>
          ))}
        </div>

        <div className="flex flex-col gap-1.5">
          {members.map((m, i) => {
            const tint = MEMBER_TINTS[i];
            const arrive = m.legs.length ? m.legs[m.legs.length - 1].arrive : "";
            const isActive = active === m.id;
            return (
              <motion.div
                key={m.id}
                className="grid grid-cols-[5.5rem_1fr] items-center gap-2"
                initial={reduce ? false : { opacity: 0, y: 8 }}
                whileInView={reduce ? undefined : { opacity: 1, y: 0 }}
                viewport={{ once: true, margin: "-40px" }}
                transition={{ duration: DUR.base, delay: i * 0.06, ease: EASE.standard }}
              >
                <div className="flex items-center gap-1.5 leading-tight">
                  <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: tint }} />
                  <span className="min-w-0">
                    <span className="block truncate text-xs font-medium text-zinc-700">{m.label}</span>
                    {arrive && (
                      <span className="block font-mono text-[10px] tabular-nums text-zinc-400">→ {arrive}</span>
                    )}
                  </span>
                </div>

                <div
                  className="relative h-9 rounded-md bg-zinc-50 ring-1 ring-inset ring-black/[0.04]"
                  onMouseEnter={() => setActive(m.id)}
                  onMouseLeave={() => setActive(null)}
                >
                  {m.legs.map((leg, j) => {
                    const mode = MODE[leg.mode];
                    const x0 = xPct(hoursOf(leg.depart));
                    const x1 = xPct(hoursOf(leg.arrive));
                    const caution = leg.safety === "caution";
                    return (
                      <button
                        key={j}
                        type="button"
                        onFocus={() => setActive(m.id)}
                        onBlur={() => setActive(null)}
                        aria-label={`${m.label}: ${leg.to_label}, ${mode.label}, ${leg.depart}–${leg.arrive}${caution ? ", осторожный участок" : ""}`}
                        className="group absolute top-1 flex h-7 items-center overflow-hidden rounded px-1.5 text-left transition-[box-shadow,transform] duration-150 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent active:scale-[0.99]"
                        style={{
                          left: `${x0}%`,
                          width: `${Math.max(x1 - x0, 2)}%`,
                          background: `${mode.color}${isActive ? "33" : "22"}`,
                          borderLeft: `3px solid ${mode.color}`,
                          boxShadow: isActive ? `0 0 0 1.5px ${tint}` : "none",
                          borderBottom: caution ? `2px dashed ${CAUTION}` : undefined,
                        }}
                      >
                        <span className="truncate text-[10px] font-medium text-zinc-700">
                          {leg.to_label}
                          {caution && <span className="ml-1 text-[10px] font-semibold" style={{ color: "#b45309" }}>· переход</span>}
                        </span>
                      </button>
                    );
                  })}

                  {/* Time playhead for this lane. */}
                  <div
                    aria-hidden
                    className="pointer-events-none absolute top-0 bottom-0 z-10 w-px -translate-x-1/2"
                    style={{ left: `${xPct(hour)}%`, background: "rgba(63,63,70,0.55)" }}
                  >
                    <span className="absolute -top-0.5 left-1/2 h-1.5 w-1.5 -translate-x-1/2 rounded-full" style={{ background: tint }} />
                  </div>
                </div>
              </motion.div>
            );
          })}
        </div>

        {/* Time scrubber — drag to move the whole family through the day. */}
        <div className="ml-[5.5rem] mt-2.5 flex items-center gap-2.5">
          {!reduce && (
            <button
              type="button"
              onClick={() => (playing ? setPlaying(false) : startPlay())}
              aria-label={playing ? "Пауза" : "Проиграть день"}
              aria-pressed={playing}
              className="grid h-7 w-7 shrink-0 place-items-center rounded-full border border-zinc-200 bg-white text-zinc-600 transition-colors hover:bg-zinc-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              {playing ? (
                <svg width="10" height="11" viewBox="0 0 10 11" aria-hidden="true">
                  <rect x="1" y="1" width="2.6" height="9" rx="0.6" fill="currentColor" />
                  <rect x="6.4" y="1" width="2.6" height="9" rx="0.6" fill="currentColor" />
                </svg>
              ) : (
                <svg width="10" height="11" viewBox="0 0 10 11" aria-hidden="true">
                  <path d="M1.5 1.2 8.6 5.5 1.5 9.8Z" fill="currentColor" />
                </svg>
              )}
            </button>
          )}
          <input
            type="range"
            min={DAY_START}
            max={DAY_END}
            step={0.25}
            value={hour}
            onChange={(e) => { setPlaying(false); setHour(Number(e.target.value)); }}
            aria-label="Время суток"
            aria-valuetext={fmtTime(hour)}
            className="h-1.5 flex-1 cursor-pointer appearance-none rounded-full bg-zinc-200 accent-accent focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          />
          <span className="w-11 shrink-0 text-right font-mono text-xs tabular-nums text-zinc-500">
            {fmtTime(hour)}
          </span>
        </div>

        {/* Mode legend + caution note — colour is never the only signal. */}
        <div className="ml-[5.5rem] mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-zinc-500">
          {usedModes.map((mode) => (
            <span key={mode} className="flex items-center gap-1.5">
              <span className="h-2 w-3 rounded-sm" style={{ background: `${MODE[mode].color}55`, borderLeft: `2px solid ${MODE[mode].color}` }} />
              {MODE[mode].label}
            </span>
          ))}
          {hasCaution && (
            <span className="flex items-center gap-1.5">
              <span className="h-0 w-4 border-b-2 border-dashed" style={{ borderColor: CAUTION }} />
              <span style={{ color: "#b45309" }}>осторожный участок</span>
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
