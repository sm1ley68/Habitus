"use client";
import { motion } from "framer-motion";
import PropertyCard from "./PropertyCard";
import { rankVisibleProperties } from "@/lib/map/viewport";
import { useSession } from "@/lib/store/session";

export default function PropertyList() {
  const properties = useSession((s) => s.properties);
  const viewport = useSession((s) => s.viewport);
  const mapUpdating = useSession((s) => s.mapUpdating);
  const loadingMore = useSession((s) => s.loadingMore);
  const open = useSession((s) => s.selectProperty);
  const visibleProperties = rankVisibleProperties(properties, viewport);
  return (
    <motion.div
      initial="hidden" animate="show"
      variants={{ show: { transition: { staggerChildren: 0.08 } } }}
      className="order-2 flex min-h-[320px] flex-col overflow-hidden rounded-3xl border border-zinc-200 bg-[#f8f8fa] lg:order-1 lg:min-h-0"
    >
      <div className="flex items-start justify-between gap-3 border-b border-zinc-200 bg-white px-4 py-3.5">
        <div>
          <h2 className="text-sm font-medium tracking-tight text-[#1c1d20]">
            Лучшие совпадения
          </h2>
          <p className="mt-0.5 text-xs text-zinc-400">
            {visibleProperties.length} в видимой области
          </p>
        </div>
        {(mapUpdating || loadingMore) && (
          <span className="mt-0.5 inline-flex items-center gap-1.5 text-[11px] text-zinc-400">
            <span aria-hidden className="h-3 w-3 animate-spin rounded-full border-2 border-accent/25 border-t-accent" />
            Обновляю
          </span>
        )}
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3">
        {visibleProperties.length > 0 ? visibleProperties.map(({ property, sourceIndex }) => (
          <PropertyCard
            key={property.id}
            property={property}
            index={sourceIndex}
            onOpen={open}
          />
        )) : (
          <div className="grid min-h-48 place-items-center rounded-2xl border border-dashed border-zinc-200 bg-white px-6 text-center">
            <div>
              <p className="text-sm font-medium text-zinc-700">В этой области нет подходящих квартир</p>
              <p className="mt-1.5 text-xs leading-5 text-zinc-400">
                Передвиньте карту или немного уменьшите масштаб.
              </p>
            </div>
          </div>
        )}
      </div>
    </motion.div>
  );
}
