"use client";

import { useEffect, useRef } from "react";
import { useGoogleMap } from "@/lib/map/useGoogleMap";
import { removeAdvancedMarker, toLatLng } from "@/lib/map/google";
import type { Property } from "@/lib/agent/types";

export default function MiniMap({ property }: { property: Property }) {
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, unavailable } = useGoogleMap(container, {
    interactive: false,
    zoom: 13,
    cityAware: false,
  });

  useEffect(() => {
    if (!map || !ready) return;
    map.setCenter(toLatLng(property.coordinates));
    map.setZoom(13);

    const element = document.createElement("div");
    element.className = "lmap-pin lmap-pin--home";
    element.style.setProperty("--tint", "#7C8CFF");
    element.innerHTML =
      '<span class="lmap-pin__dot"><svg viewBox="0 0 12 12" aria-hidden="true">' +
      '<path d="M2 6 L6 2.5 L10 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>' +
      '<path d="M3.2 5.5 V10 H8.8 V5.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>' +
      "</svg></span>";
    const marker = new google.maps.marker.AdvancedMarkerElement({
      map,
      position: toLatLng(property.coordinates),
      content: element,
      title: property.address || property.name,
    });
    return () => removeAdvancedMarker(marker);
  }, [map, ready, property.coordinates, property.address, property.name]);

  if (unavailable) {
    return (
      <div
        data-testid="minimap-missing-key"
        className="grid aspect-[16/9] w-full place-items-center rounded-xl bg-[#f6f7fb] px-4 text-center text-[11px] leading-relaxed text-zinc-400 ring-1 ring-inset ring-black/[0.05]"
      >
        Google Maps появится после добавления ключа
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
