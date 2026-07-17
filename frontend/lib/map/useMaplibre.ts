"use client";
import { useEffect, useRef, useState, type RefObject } from "react";
import maplibregl from "maplibre-gl";
import { mapStyleUrl } from "./style";
import { CITY_CENTER } from "./constants";
import { useSession } from "@/lib/store/session";

/**
 * Owns the MapLibre instance lifecycle for a container. Creates the map on
 * mount, tears it down on unmount, and flips `ready` once the GL style loads.
 * `missingKey` lets the caller render a graceful fallback with no map at all.
 */
export function useMaplibre(container: RefObject<HTMLDivElement | null>) {
  const [ready, setReady] = useState(false);
  const mapRef = useRef<maplibregl.Map | null>(null);
  const style = mapStyleUrl();
  const city = useSession((s) => s.city);

  useEffect(() => {
    if (!container.current || !style) return;
    const map = new maplibregl.Map({
      container: container.current,
      style,
      center: CITY_CENTER[city],
      zoom: 12.5,
      // Slight negative bearing gives the canvas a touch of cinematic depth
      // without disorienting a real-estate reader.
      bearing: -6,
      pitch: 0,
      attributionControl: { compact: true },
      // Smoother wheel/zoom feel than the snappy default.
      dragRotate: true,
      fadeDuration: 240,
    });
    mapRef.current = map;
    map.on("load", () => setReady(true));
    return () => { map.remove(); mapRef.current = null; setReady(false); };
    // style is derived from env; intentionally run once per mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { map: mapRef.current, ready, missingKey: !style };
}
