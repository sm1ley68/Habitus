"use client";
import { useEffect, useRef } from "react";
import maplibregl from "maplibre-gl";
import { useMaplibre } from "@/lib/map/useMaplibre";
import { LOGISTICS_HOME } from "@/lib/data/mock";
import { DUR } from "@/lib/motion";
import type { Destination, DestinationKind } from "@/lib/agent/types";
import type { VizProps } from "./index";

// The one saturated brand color allowed on the neutral canvas — every road
// route draws in it. Destination kinds get their own soft, desaturated tints on
// the pins only, so the accent stays the loudest thing.
const ACCENT = "#7C8CFF";
const KIND_TINT: Record<DestinationKind, string> = {
  school: "#6f7cc8",
  metro: "#6f9bc0",
  work: "#8b93bb",
  park: "#6f9e79",
};

// 12×12 line glyphs drawn with currentColor so the pin's --tint drives them.
const ICON: Record<DestinationKind | "home", string> = {
  home: '<path d="M2 6 L6 2.5 L10 6" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/><path d="M3.2 5.5 V10 H8.8 V5.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/>',
  school: '<path d="M1.5 5 L6 3 L10.5 5 L6 7 Z" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/><path d="M9 5.6 V8 C9 9 3 9 3 8 V5.6" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>',
  metro: '<path d="M2.4 9 V3.2 L6 6.6 L9.6 3.2 V9" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>',
  work: '<rect x="2.2" y="4.2" width="7.6" height="5.4" rx="1" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M4.6 4.2 V3 C4.6 2.5 7.4 2.5 7.4 3 V4.2" fill="none" stroke="currentColor" stroke-width="1.3"/>',
  park: '<path d="M6 10 V6.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/><circle cx="6" cy="4.4" r="2.6" fill="none" stroke="currentColor" stroke-width="1.3"/>',
};

const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

const easeOutCubic = (t: number) => 1 - Math.pow(1 - t, 3);

type LngLat = [number, number];

// Equirectangular metre distance — plenty accurate over the ~1 km these
// walking routes span, and cheap enough to run per animation frame.
function segMetres(a: LngLat, b: LngLat): number {
  const R = 6371000;
  const lat = ((a[1] + b[1]) / 2) * (Math.PI / 180);
  const dLat = (b[1] - a[1]) * (Math.PI / 180);
  const dLng = (b[0] - a[0]) * (Math.PI / 180) * Math.cos(lat);
  return R * Math.hypot(dLat, dLng);
}

// Precompute cumulative length so we can reveal a route by distance-fraction and
// place a traveller exactly on the line (not on a chord).
function prepare(coords: LngLat[]) {
  const cum = [0];
  for (let i = 1; i < coords.length; i++) cum.push(cum[i - 1] + segMetres(coords[i - 1], coords[i]));
  return { coords, cum, total: cum[cum.length - 1] || 1 };
}

function sliceAt(p: ReturnType<typeof prepare>, t: number): { line: LngLat[]; head: LngLat } {
  if (t >= 1) return { line: p.coords, head: p.coords[p.coords.length - 1] };
  const target = p.total * t;
  const line: LngLat[] = [p.coords[0]];
  for (let i = 1; i < p.coords.length; i++) {
    if (p.cum[i] < target) { line.push(p.coords[i]); continue; }
    const seg = p.cum[i] - p.cum[i - 1];
    const f = seg > 0 ? (target - p.cum[i - 1]) / seg : 0;
    const a = p.coords[i - 1], b = p.coords[i];
    const head: LngLat = [a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f];
    line.push(head);
    return { line, head };
  }
  return { line: p.coords, head: p.coords[p.coords.length - 1] };
}

function lineFeature(coords: LngLat[]): GeoJSON.Feature {
  return { type: "Feature", properties: {}, geometry: { type: "LineString", coordinates: coords } };
}

// Road-following walking route from the public OSRM demo (CORS-enabled). Falls
// back to a straight home→dest line so the viz never breaks offline; walking
// time then estimated at ~1.35 m/s.
async function fetchRoute(home: LngLat, dest: LngLat): Promise<{ coords: LngLat[]; minutes: number }> {
  const url = `https://router.project-osrm.org/route/v1/foot/${home[0]},${home[1]};${dest[0]},${dest[1]}?overview=full&geometries=geojson`;
  try {
    const res = await fetch(url);
    if (!res.ok) throw new Error(String(res.status));
    const data = await res.json();
    const r = data?.routes?.[0];
    const coords = r?.geometry?.coordinates as LngLat[] | undefined;
    if (!coords?.length) throw new Error("empty");
    return { coords, minutes: Math.max(1, Math.round(r.duration / 60)) };
  } catch {
    return { coords: [home, dest], minutes: Math.max(1, Math.round(segMetres(home, dest) / (1.35 * 60))) };
  }
}

function makePin(kind: DestinationKind | "home", label: string, minutes: number | null, index: number): HTMLDivElement {
  const el = document.createElement("div");
  el.className = `lmap-pin lmap-pin--${kind}`;
  el.style.setProperty("--i", String(index));
  el.style.setProperty("--tint", kind === "home" ? ACCENT : KIND_TINT[kind]);
  const time = minutes != null ? ` · <b>${minutes} мин</b>` : "";
  el.innerHTML =
    `<span class="lmap-pin__dot"><svg viewBox="0 0 12 12" aria-hidden="true">${ICON[kind]}</svg></span>` +
    `<span class="lmap-pin__label">${label}${time}</span>`;
  return el;
}

export default function LogisticsMap({ metrics, destinations }: VizProps) {
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, missingKey } = useMaplibre(container);
  const dests = destinations ?? [];
  const walk = Number(metrics.walkMinutes ?? 0);

  useEffect(() => {
    if (!map || !ready) return;
    const home = LOGISTICS_HOME;
    const reduced = prefersReducedMotion();

    // Calm viz: page scroll and rotation win; a gentle pan is still allowed.
    map.scrollZoom.disable();
    map.doubleClickZoom.disable();
    map.dragRotate.disable();
    map.touchZoomRotate.disableRotation();

    let cancelled = false;
    const rafs: number[] = [];
    const markers: maplibregl.Marker[] = [];
    const layerIds: string[] = [];

    // Frame the door plus every destination.
    const bounds = [home, ...dests.map((d) => d.coordinates)].reduce(
      (b, c) => b.extend(c),
      new maplibregl.LngLatBounds(home, home),
    );
    map.fitBounds(bounds, {
      padding: { top: 46, bottom: 46, left: 46, right: 90 },
      duration: reduced ? 0 : DUR.slow * 1000,
      maxZoom: 15,
    });

    // The door — dropped in immediately as the anchor of the whole picture.
    markers.push(
      new maplibregl.Marker({ element: makePin("home", "Дом", null, 0), anchor: "center" })
        .setLngLat(home)
        .addTo(map),
    );

    dests.forEach((d: Destination, i) => {
      const sourceId = `route-${i}`;
      const layerId = `route-line-${i}`;

      fetchRoute(home, d.coordinates).then(({ coords, minutes }) => {
        if (cancelled || !map.getStyle()) return;

        if (!map.getSource(sourceId)) {
          map.addSource(sourceId, { type: "geojson", data: lineFeature([coords[0]]) });
          map.addLayer({
            id: layerId,
            type: "line",
            source: sourceId,
            layout: { "line-cap": "round", "line-join": "round" },
            paint: { "line-color": ACCENT, "line-width": 3, "line-blur": 0.3, "line-opacity": 0.9 },
          });
          layerIds.push(layerId);
        }
        const source = map.getSource(sourceId) as maplibregl.GeoJSONSource;

        // Destination pin with its real walking time.
        markers.push(
          new maplibregl.Marker({ element: makePin(d.kind, d.label, minutes, i + 1), anchor: "center" })
            .setLngLat(d.coordinates)
            .addTo(map),
        );

        const prepped = prepare(coords);

        if (reduced) {
          source.setData(lineFeature(coords));
          return;
        }

        // A dot rides the road as the line draws itself behind it.
        const traveller = new maplibregl.Marker({
          element: Object.assign(document.createElement("div"), { className: "lmap-traveller" }),
          anchor: "center",
        })
          .setLngLat(coords[0])
          .addTo(map);

        const dur = DUR.cinematic * 1000;
        const delay = i * 260 + 220;
        const start = performance.now();
        const step = (now: number) => {
          if (cancelled) return;
          const t = Math.min(1, Math.max(0, (now - start - delay) / dur));
          const { line, head } = sliceAt(prepped, easeOutCubic(t));
          source.setData(lineFeature(line));
          traveller.setLngLat(head);
          if (t < 1) {
            rafs.push(requestAnimationFrame(step));
          } else {
            traveller.remove();
          }
        };
        rafs.push(requestAnimationFrame(step));
      });
    });

    return () => {
      cancelled = true;
      rafs.forEach(cancelAnimationFrame);
      markers.forEach((m) => m.remove());
      // Map may already be torn down by useMaplibre's own cleanup.
      try {
        layerIds.forEach((id) => {
          if (map.getLayer(id)) map.removeLayer(id);
          const src = id.replace("route-line-", "route-");
          if (map.getSource(src)) map.removeSource(src);
        });
      } catch { /* map gone */ }
    };
    // dests is stable mock data; run once the GL style is ready.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, ready]);

  if (missingKey) {
    return (
      <div
        data-testid="logistics-missing-key"
        className="grid h-56 w-full place-items-center rounded-2xl bg-[#f6f7fb] px-6 text-center text-sm text-zinc-400 ring-1 ring-inset ring-black/[0.05]"
      >
        Карта маршрутов появится с ключом MapTiler
      </div>
    );
  }

  return (
    <div data-testid="logistics-map" className="overflow-hidden rounded-2xl ring-1 ring-inset ring-black/[0.06]">
      <div className="relative h-56 w-full bg-[#f6f7fb]">
        <div ref={container} className="absolute inset-0" />
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 shadow-[inset_0_0_0_1px_rgba(20,20,34,0.05)]"
        />
      </div>
      <div className="flex items-center justify-between border-t border-zinc-100 bg-white px-3.5 py-2 text-xs text-zinc-500">
        <span className="flex items-center gap-2">
          <span className="h-[3px] w-6 rounded-full" style={{ background: ACCENT }} />
          Пешие маршруты по дорогам
        </span>
        <span>
          <span className="font-mono text-accent">{walk} мин</span>
          <span className="ml-1.5 text-zinc-400">до Лицея 239</span>
        </span>
      </div>
    </div>
  );
}
