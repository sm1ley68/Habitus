"use client";
import { motion } from "framer-motion";
import MatchScore from "@/components/result/MatchScore";
import { money } from "@/lib/format";
import { DUR, SPRING } from "@/lib/motion";

// Тот же критерий, что и в MapCanvas: framer-motion не отключает анимацию сам.
const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

// Превью объекта прямо на карте — общая карточка для двух источников:
// объектов подбора и любых объявлений слоя карты.
//
// match_score рисуется ТОЛЬКО когда он есть: процент совпадения привязан к
// запросу, и у объекта, открытого с карты вне подбора, его не существует —
// нарисовать «0%» значило бы выдумать факт.
export type PreviewData = {
  id: string;
  name: string;
  address?: string;
  cover_image: string;
  price_from: number | null;
  match_score?: number | null;
  tags?: string[];
};

export default function MapPreviewCard({
  data,
  anchor,
  onOpen,
  onMouseEnter,
  onMouseLeave,
  onClose,
}: {
  data: PreviewData;
  anchor: { x: number; y: number };
  onOpen: () => void;
  onMouseEnter?: () => void;
  onMouseLeave?: () => void;
  onClose?: () => void;
}) {
  const reduce = prefersReducedMotion();
  const title = data.address || data.name;

  return (
    <motion.button
      key={data.id}
      type="button"
      onClick={onOpen}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      onKeyDown={(e) => { if (e.key === "Escape") onClose?.(); }}
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
      aria-label={`${title} — открыть карточку`}
    >
      <div className="relative w-full aspect-[3/2] overflow-hidden bg-zinc-100">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={data.cover_image}
          alt={title}
          loading="lazy"
          className="absolute inset-0 h-full w-full object-cover"
        />
        <div className="absolute inset-0 bg-gradient-to-t from-black/45 via-black/0 to-black/5" />
        {typeof data.match_score === "number" && (
          <div className="absolute right-2.5 top-2.5">
            <MatchScore value={data.match_score} />
          </div>
        )}
      </div>
      <div className="p-3.5">
        <h3 className="line-clamp-2 text-sm font-medium tracking-tight text-[#1c1d20]">
          {title}
        </h3>
        <p className="mt-1 font-mono text-sm text-zinc-700">{money(data.price_from)}</p>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {(data.tags ?? []).slice(0, 2).map((t) => (
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
      {/* Уголок, указывающий на точку. */}
      <span
        aria-hidden
        className="absolute left-1/2 top-full h-3 w-3 -translate-x-1/2 -translate-y-1/2 rotate-45 border-b border-r border-zinc-200 bg-white"
      />
    </motion.button>
  );
}
