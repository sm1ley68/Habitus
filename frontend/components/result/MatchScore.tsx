"use client";
import { motion, useReducedMotion } from "framer-motion";
import { DUR, EASE } from "@/lib/motion";

const R = 16;
const C = 2 * Math.PI * R;

export default function MatchScore({ value }: { value: number }) {
  const shouldReduceMotion = useReducedMotion();
  const dash = (Math.max(0, Math.min(100, value)) / 100) * C;
  const isTop = value >= 90;

  return (
    <span
      role="img"
      aria-label={`${value}% совпадение`}
      className="relative inline-grid place-items-center w-11 h-11 shrink-0 rounded-full bg-black/25 backdrop-blur-[6px] ring-1 ring-white/15"
      style={
        isTop
          ? { boxShadow: "0 0 0 1px rgba(124,140,255,0.25), 0 4px 14px -4px rgba(124,140,255,0.55)" }
          : undefined
      }
    >
      <svg width="44" height="44" viewBox="0 0 44 44" className="-rotate-90">
        <circle
          cx="22" cy="22" r={R} fill="none"
          stroke="currentColor" strokeOpacity="0.25" strokeWidth="3"
          className="text-white"
        />
        <motion.circle
          cx="22" cy="22" r={R} fill="none"
          stroke="currentColor" strokeWidth="3" strokeLinecap="round"
          className="text-white"
          style={{ strokeDasharray: C }}
          initial={shouldReduceMotion ? false : { strokeDashoffset: C }}
          animate={{ strokeDashoffset: C - dash }}
          transition={
            shouldReduceMotion
              ? { duration: 0 }
              : { duration: DUR.slow, ease: EASE.emphasizedDecelerate, delay: 0.1 }
          }
        />
      </svg>
      <span className="absolute font-mono text-[11px] font-medium tabular-nums text-white">
        {Math.round(value)}
      </span>
    </span>
  );
}
