"use client";
import { motion, useReducedMotion } from "framer-motion";
import { SPRING } from "@/lib/motion";
import type { BriefItem, BriefStatus } from "@/lib/agent/types";

// Screen ② — the investigation brief. Every criterion from the request as a chip;
// its status is carried by BOTH a glyph and a tint (never colour alone), and the
// text label is always present. met=accent check, compromise=amber warning,
// relaxed=blue arrows (search widened), unknown=zinc question.
type StatusStyle = {
  ring: string;
  tint: string;
  glyph: React.ReactNode;
  srLabel: string;
};

const STATUS: Record<BriefStatus, StatusStyle> = {
  met: {
    ring: "border-[#7C8CFF]/35 bg-[#7C8CFF]/8",
    tint: "#5b6bd6",
    srLabel: "выполнено",
    glyph: <path d="M3.5 8.2 6.4 11l6.1-6.4" />,
  },
  compromise: {
    ring: "border-amber-400/45 bg-amber-50",
    tint: "#b3822f",
    srLabel: "компромисс",
    glyph: <path d="M8 4.2v4.4M8 11.2v.2M8 1.8 1.6 12.8h12.8L8 1.8Z" />,
  },
  relaxed: {
    ring: "border-sky-400/40 bg-sky-50",
    tint: "#3f74b0",
    srLabel: "критерий смягчён",
    glyph: <path d="M9.5 3 13 6.5 9.5 10M13 6.5H4.5M6.5 13 3 9.5" />,
  },
  unknown: {
    ring: "border-zinc-300 bg-zinc-50",
    tint: "#71717a",
    srLabel: "требует уточнения",
    glyph: <path d="M6 6a2 2 0 1 1 2.6 1.9c-.6.2-.9.6-.9 1.3v.4M8 12v.2" />,
  },
};

export default function BriefStrip({ brief }: { brief: BriefItem[] }) {
  const reduce = useReducedMotion();
  if (!brief.length) return null;

  return (
    <section className="border-t border-zinc-100 bg-white">
      <div className="mx-auto w-full max-w-5xl px-6 py-8">
        <h2 className="text-[11px] font-medium uppercase tracking-[0.2em] text-zinc-400">
          Бриф расследования
        </h2>
        <motion.ul
          initial="hidden"
          whileInView="show"
          viewport={{ once: true, margin: "-40px" }}
          variants={{ show: { transition: { staggerChildren: reduce ? 0 : 0.05 } } }}
          className="mt-4 flex flex-wrap gap-2.5"
        >
          {brief.map((item, i) => {
            const s = STATUS[item.status] ?? STATUS.unknown;
            return (
              <motion.li
                key={`${item.label}-${i}`}
                variants={{ hidden: reduce ? {} : { opacity: 0, y: 8 }, show: { opacity: 1, y: 0 } }}
                transition={SPRING.soft}
                whileHover={reduce ? undefined : { y: -2, scale: 1.015 }}
                className={`inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-sm text-[#1c1d20] transition-shadow duration-200 hover:shadow-[0_6px_16px_-8px_rgba(28,29,32,0.3)] ${s.ring}`}
              >
                <svg
                  width="15"
                  height="15"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke={s.tint}
                  strokeWidth="1.6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                  className="shrink-0"
                >
                  {s.glyph}
                </svg>
                <span>{item.label}</span>
                <span className="sr-only"> — {s.srLabel}</span>
              </motion.li>
            );
          })}
        </motion.ul>
      </div>
    </section>
  );
}
