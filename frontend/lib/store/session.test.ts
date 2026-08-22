import { vi } from "vitest";
import { useSession } from "./session";
import type { AgentEvent } from "@/lib/agent/types";
import { ZONE_GEOJSON, runResult } from "@/test/fixtures";

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
  useSession.getState().finish(runResult({ properties: [{ name: "X" } as never] }));
  expect(useSession.getState().properties).toHaveLength(1);
  expect(useSession.getState().screen).toBe("result");
  expect(useSession.getState().stage).toBe("done");
});

test("finish stores the chat id for the passport seam", () => {
  useSession.getState().finish(runResult({ chatId: "c-42" }));
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
  useSession.getState().finish(runResult({ zoneGeoJSON: ZONE_GEOJSON }));
  expect(useSession.getState().zoneGeoJSON).toBe(ZONE_GEOJSON);
});

it("finish() leaves the zone empty when the backend sent none", () => {
  useSession.getState().reset();
  useSession.getState().finish(runResult());
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

test("тумблера «Плотность баров» больше нет — один контрол на одну сущность", async () => {
  const { MAP_LAYER_IDS, LAYER_LABELS } = await import("@/lib/agent/types");
  expect(MAP_LAYER_IDS).not.toContain("crime");
  expect(Object.values(LAYER_LABELS)).not.toContain("Плотность баров");
  expect(MAP_LAYER_IDS).toContain("bars");
});

test("тумблер «Бары» поднимает и точки, и скопления вокруг них", () => {
  useSession.setState({ activeLayers: { ...useSession.getState().activeLayers, bars: false, crime: false } });
  useSession.getState().toggleLayer("bars");
  const on = useSession.getState().activeLayers;
  expect(on.bars).toBe(true);
  expect(on.crime).toBe(true);   // буферы — часть того же слоя, не отдельный

  useSession.getState().toggleLayer("bars");
  const off = useSession.getState().activeLayers;
  expect(off.bars).toBe(false);
  expect(off.crime).toBe(false);
});

// --- многоходовый диалог и «показать ещё» ---------------------------------

test("следующий запрос уходит в тот же чат", async () => {
  // Без этого шлюз не найдёт разбор предыдущего шага, и «подешевле»
  // разберётся с нуля — многоходовый чат не включится никогда.
  useSession.getState().finish(runResult({ chatId: "c-7" }));

  let seen: { chatId?: string } | undefined;
  const client = {
    run: (_q: string, _h: never, options?: { chatId?: string }) => {
      seen = options;
      return () => {};
    },
  };
  useSession.getState().startQuery(client as never, "подешевле");

  expect(seen?.chatId).toBe("c-7");
});

test("первый запрос идёт без чата", () => {
  let seen: { chatId?: string } | undefined = { chatId: "нетронуто" };
  const client = {
    run: (_q: string, _h: never, options?: { chatId?: string }) => {
      seen = options;
      return () => {};
    },
  };
  useSession.getState().startQuery(client as never, "тихая двушка");

  expect(seen).toBeUndefined();
});

test("смена города обрывает диалог", () => {
  // Чат привязан к городу на бэке: продолжать московский диалог питерской
  // репликой нельзя.
  useSession.getState().finish(runResult({ chatId: "c-msk" }));
  useSession.getState().setCity("spb");

  expect(useSession.getState().chatId).toBeNull();
});

test("«новый поиск» начинает разговор заново", () => {
  // Кнопка в LeftRail зовёт reset() — он обязан оборвать и диалог, иначе
  // «новый поиск» продолжал бы старый контекст.
  useSession.getState().finish(runResult({ chatId: "c-1", total: 30, hasMore: true }));
  useSession.getState().reset();

  const s = useSession.getState();
  expect(s.chatId).toBeNull();
  expect(s.hasMore).toBe(false);
  expect(s.properties).toEqual([]);
});

test("loadMore дописывает страницу к уже показанным", async () => {
  useSession.getState().finish(runResult({
    properties: [{ id: "A" } as never], chatId: "c-1", total: 3, hasMore: true,
  }));
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ objects: [{ id: "B" }, { id: "C" }], count: 2, total: 3 }),
  }));

  await useSession.getState().loadMore();

  const s = useSession.getState();
  expect(s.properties.map((p) => p.id)).toEqual(["A", "B", "C"]);
  expect(s.hasMore).toBe(false);   // добрали до total — кнопки больше нет
  expect(s.loadingMore).toBe(false);
  vi.unstubAllGlobals();
});

test("loadMore просит остаток от уже показанного, а не с нуля", async () => {
  useSession.getState().finish(runResult({
    properties: [{ id: "A" } as never, { id: "B" } as never], chatId: "c-1",
    total: 30, hasMore: true,
  }));
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true, json: async () => ({ objects: [], count: 0, total: 30 }),
  });
  vi.stubGlobal("fetch", fetchMock);

  await useSession.getState().loadMore();

  expect(String(fetchMock.mock.calls[0][0])).toContain("offset=2");
  vi.unstubAllGlobals();
});

test("сбой догрузки не теряет показанное и прячет кнопку", async () => {
  useSession.getState().finish(runResult({
    properties: [{ id: "A" } as never], chatId: "c-1", total: 30, hasMore: true,
  }));
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("сеть")));

  await useSession.getState().loadMore();

  const s = useSession.getState();
  expect(s.properties).toHaveLength(1);   // то, что было, осталось
  expect(s.hasMore).toBe(false);          // повторный тык по мёртвой кнопке хуже её отсутствия
  expect(s.loadingMore).toBe(false);
  vi.unstubAllGlobals();
});

test("loadMore молчит, когда тянуть нечего", async () => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);

  await useSession.getState().loadMore();          // ни чата, ни hasMore

  expect(fetchMock).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});
