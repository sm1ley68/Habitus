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
    properties: [{ name: "X" } as never], zoneGeoJSON: null, areaLabel: null, chatId: "c1",
  });
  expect(useSession.getState().properties).toHaveLength(1);
  expect(useSession.getState().screen).toBe("result");
  expect(useSession.getState().stage).toBe("done");
});

test("finish stores the chat id for the passport seam", () => {
  useSession.getState().finish({ properties: [], zoneGeoJSON: null, areaLabel: null, chatId: "c-42" });
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
  useSession.getState().finish({ properties: [], zoneGeoJSON: ZONE_GEOJSON, areaLabel: null, chatId: "c1" });
  expect(useSession.getState().zoneGeoJSON).toBe(ZONE_GEOJSON);
});

it("finish() leaves the zone empty when the backend sent none", () => {
  useSession.getState().reset();
  useSession.getState().finish({ properties: [], zoneGeoJSON: null, areaLabel: null, chatId: "c1" });
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

test("объект, открытый с карты, показывается в паспорте без контекста чата", () => {
  const fromMap = {
    id: "cian_42", name: "2-комн, 40 м²", address: "Москва, Снежная улица, 4",
    cover_image: "https://cdn/a.jpg", match_score: 0, price_from: 21_300_000,
    rooms: 2, area_sqm: 40, floor: "", tags: [],
    coordinates: [37.62, 55.75] as [number, number],
  };
  useSession.getState().openListingFromMap(fromMap);
  expect(useSession.getState().mapProperty?.id).toBe("cian_42");
  expect(useSession.getState().screen).toBe("passport");

  // возврат к выдаче обязан снять карточку с карты, иначе она перекроет
  // обычный выбор из результатов подбора
  useSession.getState().setScreen("result");
  expect(useSession.getState().mapProperty).toBeNull();
});

test("выбор объекта из выдачи сбрасывает карточку, открытую с карты", () => {
  const fromMap = {
    id: "cian_42", name: "x", address: "", cover_image: "", match_score: 0,
    price_from: null, rooms: null, area_sqm: null, floor: "", tags: [],
    coordinates: [37.62, 55.75] as [number, number],
  };
  useSession.getState().openListingFromMap(fromMap);
  useSession.getState().selectProperty(0);
  expect(useSession.getState().mapProperty).toBeNull();
});
