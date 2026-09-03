import { describe, it, expect, vi, beforeEach } from "vitest";
import { useEngagement } from "./engagement";

const api = vi.hoisted(() => ({
  listFavorites: vi.fn(), addFavorite: vi.fn(), removeFavorite: vi.fn(),
}));
const feedbackApi = vi.hoisted(() => ({ saveFeedback: vi.fn() }));
vi.mock("@/lib/api/favorites", () => api);
vi.mock("@/lib/api/feedback", () => feedbackApi);

const CARD = {
  id: "obj-1", name: "ЖК", address: "Москва", cover_image: "c.jpg",
  coordinates: [37.6, 55.7] as [number, number], price_from: 1, rooms: 2,
  area_sqm: 50, floor: "3", chat_id: null, saved_at: "2026-09-03T00:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  useEngagement.setState({
    saved: {}, favorites: [], hydrated: false, verdicts: {}, verdictsChatId: null,
  });
});

describe("engagement", () => {
  it("сохраняет объект и перечитывает список карточек с бэка", async () => {
    api.addFavorite.mockResolvedValue(undefined);
    api.listFavorites.mockResolvedValue({ objects: [CARD], count: 1, total: 1 });
    await useEngagement.getState().toggleFavorite("obj-1", "chat-1");
    expect(api.addFavorite).toHaveBeenCalledWith("obj-1", "chat-1");
    expect(useEngagement.getState().saved["obj-1"]).toBe(true);
    expect(useEngagement.getState().favorites).toHaveLength(1);
  });

  it("откатывает сердечко, когда бэк отказал", async () => {
    api.addFavorite.mockRejectedValue(new Error("нет сети"));
    await useEngagement.getState().toggleFavorite("obj-1", null);
    expect(useEngagement.getState().saved["obj-1"]).toBe(false);
  });

  it("снимает сохранение и убирает карточку из списка", async () => {
    useEngagement.setState({ saved: { "obj-1": true }, favorites: [CARD] });
    api.removeFavorite.mockResolvedValue(undefined);
    await useEngagement.getState().toggleFavorite("obj-1", null);
    expect(api.removeFavorite).toHaveBeenCalledWith("obj-1");
    expect(useEngagement.getState().favorites).toHaveLength(0);
  });

  it("оценка уходит на бэк и помнит, к какому чату относится", async () => {
    feedbackApi.saveFeedback.mockResolvedValue(undefined);
    await useEngagement.getState().rate("chat-1", "obj-1", "down", "далеко от метро");
    expect(feedbackApi.saveFeedback).toHaveBeenCalledWith(
      "chat-1", "obj-1", "down", "далеко от метро");
    expect(useEngagement.getState().verdicts["obj-1"]).toBe("down");
    expect(useEngagement.getState().verdictsChatId).toBe("chat-1");
  });

  it("откатывает оценку, когда бэк отказал", async () => {
    feedbackApi.saveFeedback.mockRejectedValue(new Error("404"));
    await useEngagement.getState().rate("chat-1", "obj-1", "up");
    expect(useEngagement.getState().verdicts["obj-1"]).toBeUndefined();
  });
});
