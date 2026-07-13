"use client";
import { motion, useReducedMotion } from "framer-motion";
import { DUR, EASE } from "@/lib/motion";
import type { VizProps } from "./index";

// A quiet equalizer: bars rise and settle to the ambient level. Lower is calmer.
const BARS = 16;

export default function NoiseViz({ metrics }: VizProps) {
  const reduce = useReducedMotion();
  const db = Number(metrics.db ?? 40);
  const level = Math.min(Math.max((db - 20) / 60, 0), 1); // 20..80 дБ -> 0..1
  const calm = db <= 40;

  return (
    <div data-testid="noise" className="rounded-xl bg-sky-50/50 p-3">
      <div className="flex items-end gap-1 h-12" aria-hidden="true">
        {Array.from({ length: BARS }).map((_, i) => {
          // A gentle standing-wave profile so the meter reads as a soft hum.
          const profile = 0.35 + 0.65 * Math.abs(Math.sin((i / BARS) * Math.PI * 1.5));
          const h = Math.max(0.14, level * profile);
          return (
            <motion.span
              key={i}
              className="flex-1 rounded-full bg-gradient-to-t from-sky-300 to-sky-400"
              style={{ transformOrigin: "bottom", height: `${h * 100}%`, scaleY: 1 }}
              initial={reduce ? false : { scaleY: 0.04, opacity: 0 }}
              whileInView={reduce ? undefined : { scaleY: 1, opacity: 1 }}
              viewport={{ once: true, margin: "-40px" }}
              transition={{
                duration: DUR.slow,
                delay: reduce ? 0 : 0.04 * i,
                ease: EASE.emphasizedDecelerate,
              }}
            />
          );
        })}
      </div>
      <p className="mt-2 flex items-baseline justify-between text-xs text-zinc-500">
        <span>Фоновый шум</span>
        <span>
          <span className="font-mono text-sky-700">{db} дБ</span>
          <span className="ml-1.5 text-zinc-400">{calm ? "тихо, как в библиотеке" : "заметный фон"}</span>
        </span>
      </p>
    </div>
  );
}
