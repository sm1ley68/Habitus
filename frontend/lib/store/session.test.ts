import { vi } from "vitest";
import { useSession } from "./session";
import type { AgentEvent } from "@/lib/agent/types";
import { ZONE_GEOJSON } from "@/test/fixtures";

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
  useSession.getState().finish({
    properties: [{ name: "X" } as never], zoneGeoJSON: null, chatId: "c1",
  });
  expect(useSession.getState().properties).toHaveLength(1);
  expect(useSession.getState().screen).toBe("result");
  expect(useSession.getState().stage).toBe("done");
});

test("finish stores the chat id for the passport seam", () => {
  useSession.getState().finish({ properties: [], zoneGeoJSON: null, chatId: "c-42" });
  expect(useSession.getState().chatId).toBe("c-42");
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

it("finish() attaches the search zone the backend sent", () => {
  useSession.getState().reset();
  useSession.getState().finish({ properties: [], zoneGeoJSON: ZONE_GEOJSON, chatId: "c1" });
  expect(useSession.getState().zoneGeoJSON).toBe(ZONE_GEOJSON);
});

it("finish() leaves the zone empty when the backend sent none", () => {
  useSession.getState().reset();
  useSession.getState().finish({ properties: [], zoneGeoJSON: null, chatId: "c1" });
  expect(useSession.getState().zoneGeoJSON).toBeNull();
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
