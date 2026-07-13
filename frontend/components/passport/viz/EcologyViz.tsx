"use client";
import { motion, useReducedMotion } from "framer-motion";
import { DUR, EASE, SPRING } from "@/lib/motion";
import type { VizProps } from "./index";

// A ring that fills to the density of nearby green spaces, plus a bar showing
// how far the nearest industrial zone sits.
const SIZE = 64, STROKE = 6, RAD = (SIZE - STROKE) / 2;

export default function EcologyViz({ metrics }: VizProps) {
  const reduce = useReducedMotion();
  const green = Number(metrics.greenSpaces ?? 0);
  const km = Number(metrics.industrialKm ?? 0);
  const ringFill = Math.min(green / 3, 1);
  const barFill = Math.min(km / 5, 1);

  return (
    <div data-testid="ecology" className="flex items-center gap-4 rounded-xl bg-emerald-50/50 p-3.5">
      {/* Ring holds ONLY the number; the descriptor sits outside, below it, so
          nothing ever collides with the stroke. */}
      <div className="flex shrink-0 flex-col items-center gap-1.5">
        <div className="relative" style={{ width: SIZE, height: SIZE }}>
          <svg width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`} aria-hidden="true">
            <circle cx={SIZE / 2} cy={SIZE / 2} r={RAD} fill="none" stroke="#d1fae5" strokeWidth={STROKE} />
            <motion.circle
              cx={SIZE / 2} cy={SIZE / 2} r={RAD}
              fill="none" stroke="#10b981" strokeWidth={STROKE} strokeLinecap="round"
              transform={`rotate(-90 ${SIZE / 2} ${SIZE / 2})`}
              style={{ pathLength: ringFill }}
              initial={reduce ? false : { pathLength: 0 }}
              whileInView={reduce ? undefined : { pathLength: ringFill }}
              viewport={{ once: true, margin: "-40px" }}
              transition={{ duration: DUR.cinematic, ease: EASE.emphasizedDecelerate }}
            />
          </svg>
          <motion.span
            className="absolute inset-0 flex items-center justify-center text-xl font-medium leading-none text-emerald-700"
            initial={reduce ? false : { opacity: 0, scale: 0.8 }}
            whileInView={reduce ? undefined : { opacity: 1, scale: 1 }}
            viewport={{ once: true, margin: "-40px" }}
            transition={{ ...SPRING.soft, delay: reduce ? 0 : 0.5 }}
          >
            {green}
          </motion.span>
        </div>
        <span className="text-[10px] uppercase tracking-wide text-emerald-600/70">зелёных зон</span>
      </div>

      <div className="flex-1 space-y-2">
        <div className="flex items-baseline justify-between text-xs">
          <span className="text-zinc-500">До промзоны</span>
          <span className="font-mono text-emerald-700">{km} км</span>
        </div>
        <span className="block h-2 overflow-hidden rounded-full bg-emerald-100">
          <motion.span
            className="block h-full rounded-full bg-emerald-500"
            style={{ transformOrigin: "left", scaleX: barFill }}
            initial={reduce ? false : { scaleX: 0 }}
            whileInView={reduce ? undefined : { scaleX: barFill }}
            viewport={{ once: true, margin: "-40px" }}
            transition={{ duration: DUR.slow, delay: reduce ? 0 : 0.2, ease: EASE.standard }}
          />
        </span>
        <p className="text-[11px] text-zinc-400">чем дальше, тем чище воздух</p>
      </div>
    </div>
  );
}
