import type { LayerId } from "@/lib/agent/types";
import { API_BASE } from "./config";

export type LayerCollections = Partial<Record<LayerId, GeoJSON.FeatureCollection>>;

// GET /geo/layers?city=&layers=a,b,c → {city, layers: {<id>: FeatureCollection}}
//
// Бэк отдаёт communal/noise/ecology пустыми FeatureCollection: под них нет
// источника (см. geo_layers_service.go — layerKinds). Пустой слой это факт
// «данных нет», а не ошибка, поэтому здесь он проходит как есть.
export async function fetchLayers(
  city: string,
  layers: LayerId[],
): Promise<LayerCollections> {
  if (!layers.length) return {};
  const res = await fetch(
    `${API_BASE}/geo/layers?city=${encodeURIComponent(city)}&layers=${layers.join(",")}`,
    { credentials: "include" },
  );
  if (!res.ok) throw new Error(`fetchLayers failed: ${res.status}`);
  const body = await res.json();
  return (body.layers ?? {}) as LayerCollections;
}
