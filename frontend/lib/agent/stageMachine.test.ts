import { nextStage } from "./stageMachine";
import type { AgentEvent } from "./types";

const ev = (agent: AgentEvent["agent"], status: AgentEvent["status"]): AgentEvent =>
  ({ agent, status, message: "" });

test("linguistic processing enters linguistic stage from idle", () => {
  expect(nextStage("idle", ev("linguistic", "processing"))).toBe("linguistic");
});
test("geo processing advances to geo", () => {
  expect(nextStage("linguistic", ev("geo", "processing"))).toBe("geo");
});
test("context processing advances to context", () => {
  expect(nextStage("geo", ev("context", "processing"))).toBe("context");
});
test("relaxation_triggered enters relaxation regardless of agent", () => {
  expect(nextStage("geo", ev("orchestrator", "relaxation_triggered"))).toBe("relaxation");
});
test("orchestrator processing streams the answer", () => {
  expect(nextStage("context", ev("orchestrator", "processing"))).toBe("streaming");
});
test("orchestrator done finishes", () => {
  expect(nextStage("streaming", ev("orchestrator", "done"))).toBe("done");
});
