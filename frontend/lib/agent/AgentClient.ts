import type { StreamFailure } from "@/lib/api/streamError";
import type { AgentEvent, Property, GeoZone, ConstraintDiagnostic } from "./types";

export interface RunResult {
  properties: Property[];
  /** suggested_areas_geojson из final_result; null — бэк зону не прислал. */
  zoneGeoJSON: GeoZone | null;
  /** area_label из final_result — человекочитаемая зона («центр (ЦАО)»); null — зоны нет. */
  areaLabel: string | null;
  /** Нужен паспорту объекта и чату по объекту как контекст поиска. */
  chatId: string;
  /** Сколько объектов сохранено для этого поиска целиком (не только страница). */
  total: number;
  /** Есть ли за пределами properties ещё что показать — «показать ещё». */
  hasMore: boolean;
  /** Почему выдача пуста; непустой только при нулевой выдаче. */
  diagnostics: ConstraintDiagnostic[];
}

export interface RunOptions {
  /** Продолжить существующий диалог: реплика уходит в этот чат, и шлюз
   *  подмешает разбор предыдущего шага (prev_parsed). Без него заводится
   *  новый чат, то есть запрос разбирается без контекста. */
  chatId?: string;
}

export interface RunHandlers {
  onEvent(event: AgentEvent): void;
  onDone(result: RunResult): void;
  /** Отказ целиком: code+message, плюс cause/hint, когда шлюз их прислал. */
  onError(failure: StreamFailure): void;
}

export interface AgentClient {
  /** Starts a run; returns a cancel function that stops all pending emissions. */
  run(query: string, handlers: RunHandlers, options?: RunOptions): () => void;
}
