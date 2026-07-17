import type { AgentEvent, Property, GeoZone } from "./types";

export interface RunResult {
  properties: Property[];
  /** suggested_areas_geojson из final_result; null — бэк зону не прислал. */
  zoneGeoJSON: GeoZone | null;
  /** Нужен паспорту объекта и чату по объекту как контекст поиска. */
  chatId: string;
}

export interface RunHandlers {
  onEvent(event: AgentEvent): void;
  onDone(result: RunResult): void;
  onError(code: string, message: string): void;
}

export interface AgentClient {
  /** Starts a run; returns a cancel function that stops all pending emissions. */
  run(query: string, handlers: RunHandlers): () => void;
}
