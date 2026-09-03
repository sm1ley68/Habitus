import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import HistorySidebar from "./HistorySidebar";
import { useSession } from "@/lib/store/session";

const chatsApi = vi.hoisted(() => ({
  listChats: vi.fn(), createChat: vi.fn(), listMessages: vi.fn(),
}));
const resultsApi = vi.hoisted(() => ({ fetchMoreResults: vi.fn() }));
vi.mock("@/lib/api/chats", () => chatsApi);
vi.mock("@/lib/api/results", () => resultsApi);

const CHAT = {
  chat_id: "chat-7", city: "msk" as const, title: "Тихая двушка у школы",
  created_at: "2026-09-01T10:00:00Z",
};
const PROPERTY = {
  id: "obj-1", name: "ЖК", address: "Москва, Кирочная 12", cover_image: "c.jpg",
  match_score: 91, price_from: 1, rooms: 2, area_sqm: 50, floor: "3",
  tags: [], coordinates: [37.6, 55.7] as [number, number],
};

beforeEach(() => {
  vi.clearAllMocks();
  act(() => useSession.getState().reset());
  act(() => useSession.setState({ historyOpen: true }));
  chatsApi.listChats.mockResolvedValue([CHAT]);
});

describe("HistorySidebar", () => {
  it("клик по чату возвращает диалог и его выдачу", async () => {
    chatsApi.listMessages.mockResolvedValue([
      { message_id: "m1", role: "user", text: "тихая двушка", created_at: "" },
      { message_id: "m2", role: "assistant", text: "нашёл 3", created_at: "" },
    ]);
    resultsApi.fetchMoreResults.mockResolvedValue({
      objects: [PROPERTY], count: 1, total: 1,
    });

    render(<HistorySidebar />);
    await userEvent.click(await screen.findByText("Тихая двушка у школы"));

    const state = useSession.getState();
    // chat_id выставлен — значит следующая реплика уйдёт в ЭТОТ чат и шлюз
    // подмешает разбор предыдущего шага: диалог можно продолжать.
    expect(state.chatId).toBe("chat-7");
    expect(state.screen).toBe("result");
    expect(state.searchMessages.map((m) => m.text)).toEqual(["тихая двушка", "нашёл 3"]);
    expect(state.properties).toHaveLength(1);
    expect(state.restoring).toBe(false);
  });

  it("не выдумывает зону и диагностику, которых бэк не хранит", async () => {
    chatsApi.listMessages.mockResolvedValue([]);
    resultsApi.fetchMoreResults.mockResolvedValue({ objects: [], count: 0, total: 0 });
    act(() => useSession.setState({
      areaLabel: "Хамовники",
      zoneGeoJSON: { type: "FeatureCollection", features: [] } as never,
      diagnostics: [{ constraint: "цена", remaining: 0 }],
    }));

    render(<HistorySidebar />);
    await userEvent.click(await screen.findByText("Тихая двушка у школы"));

    const state = useSession.getState();
    expect(state.areaLabel).toBeNull();
    expect(state.zoneGeoJSON).toBeNull();
    expect(state.diagnostics).toEqual([]);
  });

  it("отказ бэка показывается ошибкой, а не пустой выдачей", async () => {
    chatsApi.listMessages.mockRejectedValue(new Error("500"));
    resultsApi.fetchMoreResults.mockResolvedValue({ objects: [], count: 0, total: 0 });

    render(<HistorySidebar />);
    await userEvent.click(await screen.findByText("Тихая двушка у школы"));

    const state = useSession.getState();
    expect(state.stage).toBe("error");
    expect(state.errorMessage).toBe("Не удалось открыть сохранённый поиск");
  });
});
