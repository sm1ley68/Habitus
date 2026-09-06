"use client";
import { EnterFullScreenIcon, ExitFullScreenIcon } from "@radix-ui/react-icons";
import { motion } from "framer-motion";
import PropertyCard from "./PropertyCard";
import { rankVisibleProperties } from "@/lib/map/viewport";
import { useSession } from "@/lib/store/session";

type PropertyListProps = {
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
};

export default function PropertyList({
  expanded = false,
  onExpandedChange,
}: PropertyListProps) {
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
      className={`flex min-h-[320px] flex-col overflow-hidden rounded-3xl border border-zinc-200 bg-[#f8f8fa] ${
        expanded ? "order-1 min-h-0" : "order-2 lg:order-1 lg:min-h-0"
      }`}
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
        <div className="flex items-center gap-2">
          {(mapUpdating || loadingMore) && (
            <span className="inline-flex items-center gap-1.5 text-[11px] text-zinc-400">
              <span aria-hidden className="h-3 w-3 animate-spin rounded-full border-2 border-accent/25 border-t-accent" />
              Обновляю
            </span>
          )}
          {onExpandedChange && (
            <button
              type="button"
              onClick={() => onExpandedChange(!expanded)}
              aria-label={expanded ? "Свернуть список квартир" : "Развернуть список квартир"}
              title={expanded ? "Свернуть список" : "Развернуть список"}
              className="grid h-8 w-8 shrink-0 place-items-center rounded-full border border-zinc-200 bg-white text-zinc-500 transition-colors hover:border-accent/35 hover:bg-accent/5 hover:text-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 active:scale-95"
            >
              {expanded ? (
                <ExitFullScreenIcon aria-hidden className="h-4 w-4" />
              ) : (
                <EnterFullScreenIcon aria-hidden className="h-4 w-4" />
              )}
            </button>
          )}
        </div>
      </div>

      <div
        className={`min-h-0 flex-1 gap-3 overflow-y-auto p-3 ${
          expanded
            ? "grid auto-rows-max grid-cols-1 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
            : "flex flex-col"
        }`}
      >
        {visibleProperties.length > 0 ? visibleProperties.map(({ property, sourceIndex }) => (
          <PropertyCard
            key={property.id}
            property={property}
            index={sourceIndex}
            onOpen={open}
          />
        )) : (
          <div className={`grid min-h-48 place-items-center rounded-2xl border border-dashed border-zinc-200 bg-white px-6 text-center ${expanded ? "col-span-full" : ""}`}>
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
