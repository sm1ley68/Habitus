"use client";
import { MAP_LAYER_IDS, LAYER_LABELS, LAYER_GEOJSON } from "@/lib/data/mock";
import { layerPaintColor } from "@/lib/map/style";
import { useSession } from "@/lib/store/session";

export default function LayerToggles() {
  const active = useSession((s) => s.activeLayers);
  const toggle = useSession((s) => s.toggleLayer);
  return (
    <div className="flex flex-wrap gap-2">
      {MAP_LAYER_IDS.map((id) => {
        const on = !!active[id];
        // Legend swatch tints to the layer's own map color so the toggle reads
        // as a key, not a generic pill.
        const swatch = layerPaintColor(LAYER_GEOJSON[id].features[0]?.geometry.type ?? "");
        return (
          <button
            key={id}
            onClick={() => toggle(id)}
            aria-pressed={on}
            className={`group inline-flex items-center gap-2 text-[13px] px-3.5 py-2 rounded-full border transition-colors duration-[240ms] ease-[cubic-bezier(0.4,0,0.2,1)] ${
              on
                ? "border-accent bg-accent/10 text-accent"
                : "border-zinc-200 text-zinc-500 hover:border-zinc-300"
            }`}
          >
            <span
              aria-hidden
              className="h-2 w-2 rounded-full transition-all duration-[240ms]"
              style={{
                backgroundColor: on ? swatch : "transparent",
                boxShadow: on ? `0 0 0 3px ${swatch}22` : "none",
                border: on ? "none" : `1.5px solid currentColor`,
                opacity: on ? 1 : 0.55,
              }}
            />
            {LAYER_LABELS[id]}
          </button>
        );
      })}
    </div>
  );
}
