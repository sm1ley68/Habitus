"use client";

import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { CITY_CENTER } from "./constants";
import { googleMapsConfig, loadGoogleMaps, toLatLng } from "./google";
import { useSession } from "@/lib/store/session";

type Options = {
  interactive?: boolean;
  zoom?: number;
  cityAware?: boolean;
};

export function useGoogleMap(
  container: RefObject<HTMLDivElement | null>,
  { interactive = true, zoom = 12.5, cityAware = true }: Options = {},
) {
  const config = useMemo(() => googleMapsConfig(), []);
  const city = useSession((state) => state.city);
  const mapRef = useRef<google.maps.Map | null>(null);
  const [map, setMap] = useState<google.maps.Map | null>(null);
  const [ready, setReady] = useState(false);
  const [loadError, setLoadError] = useState(false);

  useEffect(() => {
    if (!container.current || !config) return;
    let cancelled = false;

    void loadGoogleMaps(config)
      .then(({ maps }) => {
        if (cancelled || !container.current) return;
        const instance = new maps.Map(container.current, {
          center: toLatLng(CITY_CENTER[city]),
          zoom,
          mapId: config.mapId,
          renderingType: maps.RenderingType.VECTOR,
          clickableIcons: false,
          fullscreenControl: false,
          mapTypeControl: false,
          streetViewControl: false,
          zoomControl: interactive,
          keyboardShortcuts: interactive,
          gestureHandling: interactive ? "cooperative" : "none",
          disableDefaultUI: !interactive,
          backgroundColor: "#f6f7fb",
        });
        mapRef.current = instance;
        setMap(instance);
        google.maps.event.addListenerOnce(instance, "idle", () => {
          if (!cancelled) setReady(true);
        });
      })
      .catch((error) => {
        console.error("Google Maps failed to load", error);
        if (!cancelled) setLoadError(true);
      });

    return () => {
      cancelled = true;
      if (mapRef.current) google.maps.event.clearInstanceListeners(mapRef.current);
      mapRef.current = null;
      setMap(null);
      setReady(false);
    };
    // The browser key and map id are build-time constants.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [interactive, zoom]);

  useEffect(() => {
    if (!mapRef.current || !ready || !cityAware) return;
    mapRef.current.panTo(toLatLng(CITY_CENTER[city]));
    mapRef.current.setZoom(zoom);
  }, [city, cityAware, ready, zoom]);

  return {
    map,
    ready,
    missingKey: !config,
    loadError,
    unavailable: !config || loadError,
  };
}
