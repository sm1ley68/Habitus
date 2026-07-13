"use client";
import { useEffect, useRef, useState } from "react";
import { motion, useReducedMotion, useSpring } from "framer-motion";
import { DUR, EASE, SPRING } from "@/lib/motion";
import type { VizProps } from "./index";
import type { Season, ViewClimateData, ViewType } from "@/lib/agent/types";

// A day instrument you SCRUB. Drag time from 06:00 to 20:00 and the sun rides the
// ticked arc while a warm shaft enters a schematic window and lands on the room
// floor — steep and short near noon, low and long near sunset. Inside the
// direct-light window the room fills with warm light; outside it stays cool and dim.
//
// Insolation 2.0 (when `data: ViewClimateData` is present): neighbouring buildings
// cast SHADOW WEDGES onto the arc (sun dims, no shaft) and trim the effective
// direct-light window; a SEASON selector springs the arc apex (solar altitude) and
// re-derives the direct-light readout from sun_hours_by_season; Peter's grey sky is
// an OVERCAST overlay driven by cloudiness_factor; a VIEW-TYPE badge names the vista.
const W = 300, H = 170, CX = 150, CY = 104, R = 96;
const FLOOR = H - 12;

// Window sits on the horizon (its sill IS the horizon line); the room is below it.
const WIN_W = 40, WIN_TOP = CY - 26, SILL = CY;
const WIN_L = CX - WIN_W / 2, WIN_R = CX + WIN_W / 2;
const ROOM_X0 = 40, ROOM_X1 = W - 40;

const DAY_START = 6, DAY_END = 20;
const DAY_SPAN = DAY_END - DAY_START;

function angleAt(hour: number) {
  const t = (hour - DAY_START) / DAY_SPAN; // 0..1
  return Math.PI * (1 - t); // left horizon (pi) -> right horizon (0)
}
// `apex` (0..1) scales the vertical radius = seasonal solar altitude.
function arcPoint(hour: number, apex = 1) {
  const a = angleAt(hour);
  return { x: CX + R * Math.cos(a), y: CY - R * apex * Math.sin(a) };
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));
const pad2 = (n: number) => String(n).padStart(2, "0");
function fmtTime(hour: number) {
  const h = Math.floor(hour);
  const m = Math.round((hour - h) * 60);
  return `${pad2(h)}:${pad2(m === 60 ? 0 : m)}`;
}
// "14:00" -> 14 (fractional hours); tolerant of a bare number.
function parseHour(v: string | undefined, fallback: number) {
  if (!v) return fallback;
  const [h, m] = v.split(":");
  const hh = Number(h);
  if (!Number.isFinite(hh)) return fallback;
  return hh + (Number(m) || 0) / 60;
}

// 8 compass points from degrees; 225 -> ЮЗ.
const COMPASS = ["С", "СВ", "В", "ЮВ", "Ю", "ЮЗ", "З", "СЗ"];
function compass(deg: number) {
  return COMPASS[Math.round((deg % 360) / 45) % 8];
}

// hex lerp for warmth: low sun -> orange, high sun -> amber.
function hex(n: number) {
  return clamp(Math.round(n), 0, 255).toString(16).padStart(2, "0");
}
function mix(a: string, b: string, t: number) {
  const pa = [1, 3, 5].map((i) => parseInt(a.slice(i, i + 2), 16));
  const pb = [1, 3, 5].map((i) => parseInt(b.slice(i, i + 2), 16));
  return `#${hex(pa[0] + (pb[0] - pa[0]) * t)}${hex(pa[1] + (pb[1] - pa[1]) * t)}${hex(pa[2] + (pb[2] - pa[2]) * t)}`;
}

// Season -> relative solar-noon altitude (vertical arc scale). Winter low, summer high.
const SEASON_ALT: Record<Season, number> = { winter: 0.5, spring: 0.78, summer: 1, autumn: 0.68 };
const SEASONS: { id: Season; label: string }[] = [
  { id: "winter", label: "Зима" },
  { id: "spring", label: "Весна" },
  { id: "summer", label: "Лето" },
  { id: "autumn", label: "Осень" },
];

const VIEW_LABEL: Record<ViewType, string> = {
  courtyard_park: "Двор-парк",
  street: "Улица",
  water: "Вода",
  wall: "Стена",
  well: "Колодец",
};
// Minimal SVG glyphs (no emoji) drawn in a 16×16 box, stroke = currentColor.
function ViewGlyph({ type }: { type: ViewType }) {
  const common = { fill: "none", stroke: "currentColor", strokeWidth: 1.4, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" aria-hidden="true">
      {type === "courtyard_park" && (
        <g {...common}>
          <path d="M8 13V8" />
          <circle cx="8" cy="6" r="3.4" />
          <path d="M3 13h10" />
        </g>
      )}
      {type === "street" && (
        <g {...common}>
          <path d="M4 13 6 3M12 13 10 3" />
          <path d="M8 4v2M8 8v2M8 12v0.5" />
        </g>
      )}
      {type === "water" && (
        <g {...common}>
          <path d="M2 6q2-1.6 4 0t4 0 4 0" />
          <path d="M2 10q2-1.6 4 0t4 0 4 0" />
        </g>
      )}
      {type === "wall" && (
        <g {...common}>
          <path d="M2 5h12M2 8h12M2 11h12" />
          <path d="M6 5v3M10 8v3M6 11v0" />
        </g>
      )}
      {type === "well" && (
        <g {...common}>
          <rect x="4" y="3" width="8" height="10" rx="1" />
          <path d="M7 3v10M9 3v10" />
        </g>
      )}
    </svg>
  );
}

// Where an obstruction's azimuth lands on the day arc + how wide it shades.
// The arc parameterises azimuth ~90°(E, sunrise) -> 180°(S, midday) -> 270°(W, sunset).
// Higher elevation_deg (a taller neighbour) blocks a wider slice of sky.
function wedgeHours(azimuth_deg: number, elevation_deg: number) {
  const t = clamp((azimuth_deg - 90) / 180, 0, 1);
  const center = DAY_START + t * DAY_SPAN;
  const half = 0.75 + clamp(elevation_deg, 0, 90) / 90 * 5;
  return { center, from: center - half, to: center + half };
}

export default function InsolationViz({ metrics, data }: VizProps) {
  const reduce = useReducedMotion();

  // Narrow the hero payload; everything below stays back-compatible with metrics-only.
  const climate = data && "sun_hours_by_season" in data ? (data as ViewClimateData) : undefined;

  // Base direct-light window: from data.direct_light, else legacy metrics reads.
  const baseFrom = climate ? parseHour(climate.direct_light.from, 14) : Number(metrics.directLightFrom ?? 14);
  const baseTo = climate ? parseHour(climate.direct_light.to, 18) : Number(metrics.directLightTo ?? 18);
  const baseMid = (baseFrom + baseTo) / 2;

  const hasBearing = climate ? true : metrics.orientationDeg != null;
  const deg = climate ? climate.orientation_deg : Number(metrics.orientationDeg ?? 0);

  const wedges = climate ? climate.obstructions.map((o) => wedgeHours(o.azimuth_deg, o.elevation_deg)) : [];
  const inWedge = (h: number) => wedges.some((w) => h >= w.from && h <= w.to);

  // Season state (only exposed when we have climate data). Summer = fullest arc.
  const [season, setSeason] = useState<Season>("summer");
  const targetApex = climate ? SEASON_ALT[season] : 1;

  // Spring the arc apex on season change (transform-only feel via a scalar we recompute
  // geometry from). Reduced motion jumps straight to the settled altitude.
  const apexSpring = useSpring(targetApex, SPRING.soft);
  const [apex, setApex] = useState(targetApex);
  useEffect(() => {
    if (reduce || !climate) {
      setApex(targetApex);
      return;
    }
    apexSpring.set(targetApex);
  }, [targetApex, reduce, climate, apexSpring]);
  useEffect(() => {
    if (reduce || !climate) return;
    const unsub = apexSpring.on("change", (v) => setApex(v));
    return unsub;
  }, [apexSpring, reduce, climate]);

  // Season re-derives the direct-light window length from sun_hours_by_season,
  // centred on the base midpoint; then obstruction wedges trim the exposed edges.
  const seasonHours = climate ? climate.sun_hours_by_season[season] : baseTo - baseFrom;
  let from = clamp(baseMid - seasonHours / 2, DAY_START, DAY_END);
  let to = clamp(baseMid + seasonHours / 2, DAY_START, DAY_END);
  while (inWedge(from) && from < to) from += 0.25;
  while (inWedge(to) && to > from) to -= 0.25;
  const effectiveHours = Math.max(0, to - from);

  const mid = (from + to) / 2;

  // Settled initial frame: mid of the base direct-light window (a valid static state).
  const [hour, setHour] = useState(baseMid);
  const [playing, setPlaying] = useState(false);
  const raf = useRef<number | null>(null);
  const last = useRef<number>(0);

  // Autoplay advances time across the day; disabled under reduced motion.
  useEffect(() => {
    if (!playing || reduce) return;
    const tick = (t: number) => {
      if (!last.current) last.current = t;
      const dt = (t - last.current) / 1000;
      last.current = t;
      setHour((h) => {
        const next = h + dt * 2.4; // ~6s to cross the day
        return next >= DAY_END ? DAY_START : next;
      });
      raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
    return () => {
      if (raf.current) cancelAnimationFrame(raf.current);
      last.current = 0;
    };
  }, [playing, reduce]);

  const start = arcPoint(DAY_START, apex), end = arcPoint(DAY_END, apex);
  const lit0 = arcPoint(from, apex), lit1 = arcPoint(to, apex);
  const sun = arcPoint(hour, apex);
  const sunShadowed = inWedge(hour);

  // Direct light only while the sun faces this window AND isn't blocked by a
  // neighbour. Intensity bells to a peak mid-window and fades to nothing at edges.
  const cloud = climate ? clamp(climate.cloudiness_factor, 0, 1) : 0;
  const maxIntensity = 1 - 0.55 * cloud; // overcast lowers the ceiling
  const half = Math.max(0.5, effectiveHours / 2);
  const centered = 1 - Math.abs((hour - mid) / half);
  const inDirect = effectiveHours > 0 && hour >= from && hour <= to && !sunShadowed;
  const intensity = inDirect ? clamp(0.28 + 0.72 * centered, 0, 1) * maxIntensity : 0;

  // Warmth by elevation: high sun -> amber, low sun -> deep orange.
  const elev = clamp((CY - sun.y) / R, 0, 1);
  const sunColor = mix("#F97316", "#FBBF24", elev);
  // In shadow the sun core dims toward slate and casts nothing.
  const sunCore = sunShadowed ? "#94a3b8" : sunColor;
  const sunCoreOpacity = sunShadowed ? 0.5 : 1;

  // Light shaft: cast the window aperture into the room along the sun->window
  // direction until it hits the floor. dy>0 whenever the sun is above the horizon.
  const dx = CX - sun.x, dy = SILL - sun.y;
  const slope = dy > 0.001 ? dx / dy : 0;
  const throwY = FLOOR - SILL;
  const landL = clamp(WIN_L + 3 + slope * throwY, ROOM_X0 - 20, ROOM_X1 + 20);
  const landR = clamp(WIN_R - 3 + slope * throwY, ROOM_X0 - 20, ROOM_X1 + 20);
  const shaft = `${WIN_L + 3},${SILL} ${WIN_R - 3},${SILL} ${landR},${FLOOR} ${landL},${FLOOR}`;
  const poolX = (landL + landR) / 2;

  // Incoming ray from the sun down to the window opening (above the horizon).
  const ray = `${sun.x - 2},${sun.y} ${sun.x + 2},${sun.y} ${WIN_R - 4},${WIN_TOP} ${WIN_L + 4},${WIN_TOP}`;

  // Compass needle (top-left), pointing at the window bearing.
  const ccx = 30, ccy = 30, cr = 13;
  const rad = (deg * Math.PI) / 180;
  const nx = ccx + (cr - 4) * Math.sin(rad), ny = ccy - (cr - 4) * Math.cos(rad);
  const tx = ccx - 5 * Math.sin(rad), ty = ccy + 5 * Math.cos(rad);

  const fillT = `opacity ${DUR.base}s cubic-bezier(${EASE.standard.join(",")})`;

  return (
    <div
      data-testid="insolation"
      className="relative overflow-hidden rounded-xl bg-gradient-to-b from-amber-50/70 via-orange-50/30 to-white p-3"
    >
      {/* Warm wash that intensifies as the room fills with direct light. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[radial-gradient(120%_90%_at_50%_60%,rgba(245,166,35,0.22),transparent_62%)]"
        style={{ opacity: intensity, transition: fillT }}
      />
      {/* Overcast overlay — Peter's grey sky, driven by cloudiness_factor. */}
      {cloud > 0 && (
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 bg-[radial-gradient(130%_80%_at_50%_0%,rgba(148,163,184,0.5),transparent_70%)]"
          style={{ opacity: cloud * 0.22 }}
        />
      )}

      {climate && (
        <div className="relative mb-1 flex items-center justify-between gap-2">
          {/* Season segmented control — springs the arc apex + re-derives the readout. */}
          <div className="inline-flex gap-0.5 rounded-lg border border-amber-200/70 bg-white/70 p-0.5">
            {SEASONS.map((s) => {
              const active = s.id === season;
              return (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => setSeason(s.id)}
                  aria-pressed={active}
                  className={`rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent ${
                    active ? "bg-amber-500 text-white" : "text-amber-700/80 hover:bg-amber-50"
                  }`}
                >
                  {s.label}
                </button>
              );
            })}
          </div>
          {/* View-type badge — glyph + RU label. */}
          <span className="inline-flex items-center gap-1 rounded-md border border-zinc-200/80 bg-white/70 px-2 py-0.5 text-[11px] font-medium text-zinc-600">
            <ViewGlyph type={climate.view_type} />
            {VIEW_LABEL[climate.view_type]}
          </span>
        </div>
      )}

      <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`} className="relative w-full h-auto" aria-hidden="true">
        <defs>
          <radialGradient id="sunGlow" cx="50%" cy="50%" r="50%">
            <stop offset="0%" stopColor={sunColor} stopOpacity="0.55" />
            <stop offset="100%" stopColor={sunColor} stopOpacity="0" />
          </radialGradient>
          <linearGradient id="litArc" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0%" stopColor="#F5A623" />
            <stop offset="100%" stopColor="#F97316" />
          </linearGradient>
          <linearGradient id="shaft" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={sunColor} stopOpacity="0.62" />
            <stop offset="100%" stopColor={sunColor} stopOpacity="0" />
          </linearGradient>
        </defs>

        {/* Hour ticks + a few labels — reads as a real day timeline. */}
        {Array.from({ length: 15 }, (_, i) => DAY_START + i).map((h) => {
          const p = arcPoint(h, apex);
          const a = angleAt(h);
          const ox = Math.cos(a), oy = -Math.sin(a);
          const major = h === 6 || h === 12 || h === 18;
          return (
            <g key={h}>
              <line
                x1={p.x} y1={p.y}
                x2={p.x + ox * (major ? 6 : 3.5)} y2={p.y + oy * (major ? 6 : 3.5)}
                stroke="#e0d6c4" strokeWidth={major ? 1.6 : 1} strokeLinecap="round"
              />
              {major && (
                <text
                  x={p.x - ox * 12} y={p.y - oy * 12 + 3}
                  textAnchor="middle" fill="#b45309" fillOpacity="0.5"
                  className="font-mono" fontSize="8"
                >
                  {h}
                </text>
              )}
            </g>
          );
        })}

        {/* Shadow wedges — neighbouring buildings blocking a slice of the day. */}
        {wedges.map((w, i) => {
          const s0 = arcPoint(clamp(w.from, DAY_START, DAY_END), apex);
          const s1 = arcPoint(clamp(w.to, DAY_START, DAY_END), apex);
          return (
            <path
              key={i}
              d={`M ${CX} ${CY} L ${s0.x} ${s0.y} A ${R} ${R * apex} 0 0 1 ${s1.x} ${s1.y} Z`}
              fill="#64748b" fillOpacity="0.14" stroke="#94a3b8" strokeOpacity="0.35" strokeWidth="0.75"
            />
          );
        })}

        {/* Horizon / windowsill */}
        <line x1="12" y1={CY} x2={W - 12} y2={CY} stroke="#e6e2d8" strokeWidth="1.5" />

        {/* Hinted room the light falls into */}
        <line x1={ROOM_X0} y1={FLOOR} x2={ROOM_X1} y2={FLOOR} stroke="#e6e2d8" strokeWidth="1.5" />
        <line x1={ROOM_X0} y1={CY} x2={ROOM_X0} y2={FLOOR} stroke="#efe9dd" strokeWidth="1.2" />
        <line x1={ROOM_X1} y1={CY} x2={ROOM_X1} y2={FLOOR} stroke="#efe9dd" strokeWidth="1.2" />

        {/* Cool wash over the room when there's no direct sun. */}
        <rect
          x={ROOM_X0} y={CY} width={ROOM_X1 - ROOM_X0} height={FLOOR - CY}
          fill="#6f7cc8" style={{ opacity: (1 - intensity) * 0.12, transition: fillT }}
        />

        {/* Incoming ray: sun -> window aperture. */}
        <polygon points={ray} fill="url(#shaft)" style={{ opacity: intensity * 0.7, transition: fillT }} />
        {/* Shaft inside the room, cast to the floor. */}
        <polygon points={shaft} fill="url(#shaft)" style={{ opacity: intensity, transition: fillT }} />
        {/* Warm pool where the light lands. */}
        <ellipse
          cx={poolX} cy={FLOOR - 1} rx="24" ry="4.5" fill={sunColor}
          style={{ opacity: intensity * 0.4, transition: fillT }}
        />

        {/* Window frame with a cross mullion */}
        <rect x={WIN_L} y={WIN_TOP} width={WIN_W} height={SILL - WIN_TOP} rx="1.5"
          fill="#fff" fillOpacity="0.35" stroke="#cdbfa4" strokeWidth="1.4" />
        <line x1={CX} y1={WIN_TOP} x2={CX} y2={SILL} stroke="#cdbfa4" strokeWidth="1.1" />
        <line x1={WIN_L} y1={(WIN_TOP + SILL) / 2} x2={WIN_R} y2={(WIN_TOP + SILL) / 2} stroke="#cdbfa4" strokeWidth="1.1" />

        {/* Full day arc — faint. */}
        <path
          d={`M ${start.x} ${start.y} A ${R} ${R * apex} 0 0 1 ${end.x} ${end.y}`}
          fill="none" stroke="#e6e2d8" strokeWidth="2" strokeDasharray="2 5" strokeLinecap="round"
        />
        {/* Direct-light segment on the arc — the payoff window, drawing on view. */}
        {effectiveHours > 0 && (
          <motion.path
            d={`M ${lit0.x} ${lit0.y} A ${R} ${R * apex} 0 0 1 ${lit1.x} ${lit1.y}`}
            fill="none" stroke="url(#litArc)" strokeWidth="3.5" strokeLinecap="round"
            initial={reduce ? false : { pathLength: 0, opacity: 0 }}
            whileInView={reduce ? undefined : { pathLength: 1, opacity: 1 }}
            viewport={{ once: true, margin: "-40px" }}
            transition={{ duration: DUR.cinematic, delay: 0.25, ease: EASE.standard }}
          />
        )}

        {/* Sun — glow halo + core, positioned by the scrubbed hour. Dims in shadow. */}
        {!sunShadowed && <circle r="16" fill="url(#sunGlow)" cx={sun.x} cy={sun.y} />}
        <circle r="6.5" fill={sunCore} fillOpacity={sunCoreOpacity} cx={sun.x} cy={sun.y} />
        {!reduce && !sunShadowed && (
          <motion.circle
            r="6.5" fill="none" stroke="#FBBF24" strokeWidth="1.5"
            cx={sun.x} cy={sun.y}
            initial={{ opacity: 0 }}
            whileInView={{ opacity: [0, 0.4, 0], scale: [1, 1.9, 1] }}
            viewport={{ once: true, margin: "-40px" }}
            style={{ transformOrigin: `${sun.x}px ${sun.y}px` }}
            transition={{ duration: 2.4, repeat: Infinity, ease: EASE.glow }}
          />
        )}

        {/* Compass — reads the window bearing. */}
        {hasBearing && (
          <g>
            <circle cx={ccx} cy={ccy} r={cr} fill="#fff" fillOpacity="0.5" stroke="#e0d6c4" strokeWidth="1.2" />
            <text x={ccx} y={ccy - cr + 4.5} textAnchor="middle" fill="#a1a1aa" className="font-mono" fontSize="6.5">С</text>
            <line x1={tx} y1={ty} x2={nx} y2={ny} stroke="#F5A623" strokeWidth="1.8" strokeLinecap="round" />
            <circle cx={ccx} cy={ccy} r="1.4" fill="#F5A623" />
          </g>
        )}
      </svg>

      {/* Time scrubber — drag to watch the light enter the room. */}
      <div className="relative mt-1.5 flex items-center gap-2.5">
        {!reduce && (
          <button
            type="button"
            onClick={() => setPlaying((p) => !p)}
            aria-label={playing ? "Пауза" : "Проиграть день"}
            aria-pressed={playing}
            className="grid h-7 w-7 shrink-0 place-items-center rounded-full border border-amber-200 bg-white/80 text-amber-600 transition-colors hover:bg-amber-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
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
          className="h-1.5 flex-1 cursor-pointer appearance-none rounded-full bg-amber-100 accent-amber-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        />
        <span className="w-11 shrink-0 text-right font-mono text-xs tabular-nums text-amber-600">
          {fmtTime(hour)}
        </span>
      </div>

      <p className="relative mt-1.5 text-xs text-zinc-500">
        {sunShadowed ? (
          <span className="text-slate-500">В тени соседнего здания — прямого солнца нет</span>
        ) : inDirect ? (
          <span className="font-medium text-amber-600">Прямой свет в комнате</span>
        ) : (
          <span className="text-zinc-400">Рассеянный свет — прямое солнце в другое время</span>
        )}
      </p>
      <p className="relative mt-0.5 text-xs text-zinc-500">
        Прямое солнце{" "}
        {effectiveHours > 0 ? (
          <span className="font-medium text-amber-600">{fmtTime(from)}–{fmtTime(to)}</span>
        ) : (
          <span className="font-medium text-slate-500">затенено весь день</span>
        )}
        {climate ? ` · ${SEASONS.find((s) => s.id === season)!.label.toLowerCase()} ≈ ${seasonHours} ч` : ""}
        {hasBearing ? ` · окна на ${compass(deg)}` : ""}
      </p>
    </div>
  );
}
