"use client";
import { useEffect, useRef } from "react";
import maplibregl from "maplibre-gl";
import { mapStyleUrl } from "@/lib/map/style";
import type { Property } from "@/lib/agent/types";

// A small, fully NON-interactive live map centred on the property, marked with a
// single periwinkle pin. Replaces the old MapTiler Static-Maps <img>, which 403s
// on this key (static maps aren't in the plan). Absent key -> neutral placeholder.
export default function MiniMap({ property }: { property: Property }) {
  const container = useRef<HTMLDivElement>(null);
  const style = mapStyleUrl();
  const [lng, lat] = property.coordinates;

  useEffect(() => {
    if (!container.current || !style) return;
    const map = new maplibregl.Map({
      container: container.current,
      style,
      center: [lng, lat],
      zoom: 13,
      interactive: false, // disables dragPan / scrollZoom / rotate / keyboard
      attributionControl: { compact: true },
    });

    const el = document.createElement("div");
    el.className = "lmap-pin lmap-pin--home";
    el.style.setProperty("--tint", "#7C8CFF");
    el.innerHTML =
      '<span class="lmap-pin__dot"><svg viewBox="0 0 12 12" aria-hidden="true">' +
      '<path d="M2 6 L6 2.5 L10 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>' +
      '<path d="M3.2 5.5 V10 H8.8 V5.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>' +
      "</svg></span>";

    let marker: maplibregl.Marker | null = null;
    map.on("load", () => {
      map.resize();
      marker = new maplibregl.Marker({ element: el, anchor: "center" }).setLngLat([lng, lat]).addTo(map);
    });

    return () => { marker?.remove(); map.remove(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [style, lng, lat]);

  if (!style) {
    return (
      <div
        data-testid="minimap-missing-key"
        className="grid aspect-[16/9] w-full place-items-center rounded-xl bg-[#f6f7fb] px-4 text-center text-[11px] leading-relaxed text-zinc-400 ring-1 ring-inset ring-black/[0.05]"
      >
        Карта появится с ключом MapTiler
      </div>
    );
  }

  return (
    <div className="relative aspect-[16/9] w-full overflow-hidden rounded-xl bg-[#f6f7fb] ring-1 ring-inset ring-black/[0.05]">
      <div ref={container} className="absolute inset-0" />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 rounded-xl shadow-[inset_0_0_0_1px_rgba(20,20,34,0.06)]"
      />
    </div>
  );
}
