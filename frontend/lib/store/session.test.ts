import { vi } from "vitest";
import { useSession } from "./session";
import type { AgentEvent } from "@/lib/agent/types";
import { ZONE_GEOJSON } from "@/lib/data/mock";

const reset = () => useSession.getState().reset();
const ev = (agent: AgentEvent["agent"], status: AgentEvent["status"], token?: string): AgentEvent =>
  ({ agent, status, message: "", token });

beforeEach(reset);

test("initial state is idle chat", () => {
  const s = useSession.getState();
  expect(s.stage).toBe("idle");
  expect(s.screen).toBe("chat");
  expect(s.answer).toBe("");
});

test("applyEvent advances stage and accumulates streamed tokens", () => {
  useSession.getState().applyEvent(ev("linguistic", "processing"));
  expect(useSession.getState().stage).toBe("linguistic");
  useSession.getState().applyEvent(ev("orchestrator", "processing", "Привет"));
  useSession.getState().applyEvent(ev("orchestrator", "processing", " мир"));
  expect(useSession.getState().stage).toBe("streaming");
  expect(useSession.getState().answer).toBe("Привет мир");
});

test("finish stores properties and switches to result screen", () => {
  useSession.getState().finish([{ name: "X" } as never]);
  expect(useSession.getState().properties).toHaveLength(1);
  expect(useSession.getState().screen).toBe("result");
  expect(useSession.getState().stage).toBe("done");
});

test("toggleLayer flips a layer on and off", () => {
  useSession.getState().toggleLayer("noise");
  expect(useSession.getState().activeLayers.noise).toBe(true);
  useSession.getState().toggleLayer("noise");
  expect(useSession.getState().activeLayers.noise).toBe(false);
});

test("reset cancels an in-flight run", () => {
  const cancel = vi.fn();
  const fakeClient = { run: () => cancel } as unknown as import("@/lib/agent/AgentClient").AgentClient;
  useSession.getState().startQuery(fakeClient, "q");
  useSession.getState().reset();
  expect(cancel).toHaveBeenCalled();
});

it("finish() attaches the search zone", () => {
  useSession.getState().reset();
  useSession.getState().finish([]);
  expect(useSession.getState().zoneGeoJSON).toBe(ZONE_GEOJSON);
});
it("hovered property id round-trips", () => {
  useSession.getState().setHoveredProperty("jk-neva-residence");
  expect(useSession.getState().hoveredId).toBe("jk-neva-residence");
  useSession.getState().setHoveredProperty(null);
  expect(useSession.getState().hoveredId).toBeNull();
});
it("toggleLayer flips a typed layer id", () => {
  useSession.getState().reset();
  expect(useSession.getState().activeLayers.schools).toBe(true);
  useSession.getState().toggleLayer("schools");
  expect(useSession.getState().activeLayers.schools).toBe(false);
});
