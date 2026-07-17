"use client";
import { useEffect } from "react";
import { MAP_LAYER_IDS, LAYER_LABELS } from "@/lib/agent/types";
import { layerPaintColor } from "@/lib/map/style";
import { useSession } from "@/lib/store/session";

export default function LayerToggles() {
  const active = useSession((s) => s.activeLayers);
  const layerData = useSession((s) => s.layerData);
  const toggle = useSession((s) => s.toggleLayer);
  const loadLayer = useSession((s) => s.loadLayer);

  // Слои, включённые по умолчанию, должны приехать без клика пользователя.
  useEffect(() => {
    MAP_LAYER_IDS.filter((id) => active[id]).forEach((id) => void loadLayer(id));
    // Один раз на монтировании: дальше загрузку инициирует toggleLayer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="flex flex-wrap gap-2">
      {MAP_LAYER_IDS.map((id) => {
        const on = !!active[id];
        const data = layerData[id];
        // Бэк отдаёт communal/noise/ecology пустыми — под них нет источника
        // данных (geo_layers_service.go). Тумблер честно помечается как пустой,
        // вместо того чтобы притворяться, что слой есть.
        const empty = !!data && data.features.length === 0;
        const swatch = layerPaintColor(data?.features[0]?.geometry.type ?? "");
        return (
          <button
            key={id}
            onClick={() => toggle(id)}
            aria-pressed={on}
            title={empty ? "Нет данных по этому слою" : undefined}
            className={`group inline-flex items-center gap-2 text-[13px] px-3.5 py-2 rounded-full border transition-colors duration-[240ms] ease-[cubic-bezier(0.4,0,0.2,1)] ${
              on
                ? "border-accent bg-accent/10 text-accent"
                : "border-zinc-200 text-zinc-500 hover:border-zinc-300"
            } ${empty ? "opacity-40" : ""}`}
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
            {empty && <span className="text-[11px] text-zinc-400">нет данных</span>}
          </button>
        );
      })}
    </div>
  );
}
