"use client";
import { motion, useReducedMotion } from "framer-motion";
import MatchScore from "./MatchScore";
import { money } from "@/lib/format";
import { SPRING } from "@/lib/motion";
import { useSession } from "@/lib/store/session";
import type { Property } from "@/lib/agent/types";

export default function PropertyCard({
  property, index, onOpen,
}: { property: Property; index: number; onOpen: (i: number) => void }) {
  const setHovered = useSession((s) => s.setHoveredProperty);
  const shouldReduceMotion = useReducedMotion();

  return (
    <motion.button
      layoutId={`property-${index}`}
      onClick={() => onOpen(index)}
      onMouseEnter={() => setHovered(property.id)}
      onMouseLeave={() => setHovered(null)}
      onFocus={() => setHovered(property.id)}
      onBlur={() => setHovered(null)}
      variants={{ hidden: { opacity: 0, y: 16 }, show: { opacity: 1, y: 0 } }}
      whileHover={shouldReduceMotion ? undefined : { y: -4 }}
      whileTap={shouldReduceMotion ? undefined : { scale: 0.985 }}
      transition={SPRING.soft}
      className="group relative block w-full overflow-hidden rounded-2xl border border-zinc-200 bg-white text-left cursor-pointer transition-shadow duration-300 ease-out hover:shadow-[0_20px_44px_-24px_rgba(28,29,32,0.35)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      <div className="relative w-full aspect-[3/2] overflow-hidden bg-zinc-100">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={property.cover_image}
          alt={property.name}
          loading="lazy"
          className="absolute inset-0 h-full w-full object-cover transition-transform duration-500 ease-out group-hover:scale-[1.06]"
        />
        <div className="absolute inset-0 bg-gradient-to-t from-black/55 via-black/5 to-black/10" />
        <div className="absolute right-3 top-3">
          <MatchScore value={property.match_score} />
        </div>
      </div>

      <div className="p-5">
        <h3 className="font-medium text-[15px] tracking-tight text-[#1c1d20]">{property.name}</h3>
        <p className="mt-1.5 font-mono text-sm text-zinc-700">{money(property.price_from)}</p>
        <p className="mt-0.5 text-xs text-zinc-400">
          {property.rooms}-комн · {property.area_sqm} м² · {property.floor} этаж
        </p>
        <div className="mt-3 flex flex-wrap gap-1.5">
          {property.tags.map((t) => (
            <span
              key={t}
              className="rounded-md bg-zinc-100 px-2 py-1 text-xs text-zinc-600 transition-colors duration-150 ease-out group-hover:bg-accent/10 group-hover:text-accent"
            >
              {t}
            </span>
          ))}
        </div>
      </div>
    </motion.button>
  );
}
