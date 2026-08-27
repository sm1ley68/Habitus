import type { LayerId } from "@/lib/agent/types";
import { API_BASE } from "./config";

export type LayerCollections = Partial<Record<LayerId, GeoJSON.FeatureCollection>>;

// GET /geo/layers?city=&layers=a,b,c → {city, layers: {<id>: FeatureCollection}}
//
// Слои communal/noise/crime приходят из urban_evidence и требуют bbox: без
// вьюпорта бэк честно отдаёт пустой FeatureCollection («данных нет»), а не
// ошибку, поэтому здесь пустой слой проходит как есть.
export async function fetchLayers(
  city: string,
  layers: LayerId[],
  bbox?: [number, number, number, number],
  signal?: AbortSignal,
): Promise<LayerCollections> {
  if (!layers.length) return {};
  const bboxParam = bbox ? `&bbox=${bbox.join(",")}` : "";
  const res = await fetch(
    `${API_BASE}/geo/layers?city=${encodeURIComponent(city)}&layers=${layers.join(",")}${bboxParam}`,
    { credentials: "include", signal },
  );
  if (!res.ok) throw new Error(`fetchLayers failed: ${res.status}`);
  const body = await res.json();
  return (body.layers ?? {}) as LayerCollections;
}

export type ListingFeature = GeoJSON.Feature<GeoJSON.Point, {
  id: string; name: string; address?: string; price?: number;
  rooms?: number; area_sqm?: number; cover_image: string;
}>;

// GET /geo/listings?city=&bbox= — объявления под вьюпортом карты. Позволяет
// открыть ЛЮБОЙ объект города, а не только попавший в выдачу подбора.
// Без bbox бэк отдаёт пустую коллекцию: весь город на карту не грузится.
export async function fetchListings(
  city: string,
  bbox?: [number, number, number, number],
  signal?: AbortSignal,
): Promise<GeoJSON.FeatureCollection> {
  if (!bbox) return { type: "FeatureCollection", features: [] };
  const res = await fetch(
    `${API_BASE}/geo/listings?city=${encodeURIComponent(city)}&bbox=${bbox.join(",")}`,
    { credentials: "include", signal },
  );
  if (!res.ok) throw new Error(`fetchListings failed: ${res.status}`);
  const body = await res.json();
  return (body.listings ?? { type: "FeatureCollection", features: [] }) as GeoJSON.FeatureCollection;
}
