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
): Promise<LayerCollections> {
  if (!layers.length) return {};
  const bboxParam = bbox ? `&bbox=${bbox.join(",")}` : "";
  const res = await fetch(
    `${API_BASE}/geo/layers?city=${encodeURIComponent(city)}&layers=${layers.join(",")}${bboxParam}`,
    { credentials: "include" },
  );
  if (!res.ok) throw new Error(`fetchLayers failed: ${res.status}`);
  const body = await res.json();
  return (body.layers ?? {}) as LayerCollections;
}
