import type { AgentEvent, Property } from "./types";

export interface RunHandlers {
  onEvent(event: AgentEvent): void;
  onDone(result: { properties: Property[] }): void;
}

export interface AgentClient {
  /** Starts a run; returns a cancel function that stops all pending emissions. */
  run(query: string, handlers: RunHandlers): () => void;
}
