import type { Stage, AgentEvent } from "./types";

export function nextStage(current: Stage, event: AgentEvent): Stage {
  if (event.status === "relaxation_triggered") return "relaxation";
  if (event.status === "done") return "done";
  switch (event.agent) {
    case "linguistic": return "linguistic";
    case "geo": return "geo";
    case "context": return "context";
    case "orchestrator": return "streaming";
    default: return current;
  }
}
