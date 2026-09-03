import { create } from "zustand";
import type { FavoriteObject, FeedbackVerdict } from "@/lib/agent/types";
import { addFavorite, listFavorites, removeFavorite } from "@/lib/api/favorites";
import { saveFeedback } from "@/lib/api/feedback";

interface EngagementState {
  /** Сохранённые объекты по id. Ключа нет → объект не сохранён. */
  saved: Record<string, boolean>;
  /** Полные карточки избранного — нужны экрану «Сохранённое». */
  favorites: FavoriteObject[];
  hydrated: boolean;
  /** Оценки текущего чата: object_id → вердикт. Оценка принадлежит паре
   *  (чат, объект), поэтому при смене чата словарь обнуляется. */
  verdicts: Record<string, FeedbackVerdict>;
  verdictsChatId: string | null;

  hydrate: () => Promise<void>;
  toggleFavorite: (objectId: string, chatId?: string | null) => Promise<void>;
  rate: (chatId: string, objectId: string, verdict: FeedbackVerdict, reason?: string) => Promise<void>;
  resetVerdicts: (chatId: string | null) => void;
}

let hydration: Promise<void> | null = null;

export const useEngagement = create<EngagementState>((set, get) => ({
  saved: {},
  favorites: [],
  hydrated: false,
  verdicts: {},
  verdictsChatId: null,

  // Один запрос на сессию: избранное меняется только отсюда, поэтому
  // перечитывать его на каждое открытие выдачи незачем.
  hydrate: () => {
    if (hydration) return hydration;
    if (get().hydrated) return Promise.resolve();
    hydration = (async () => {
      try {
        const page = await listFavorites(100, 0);
        set({
          favorites: page.objects,
          saved: Object.fromEntries(page.objects.map((o) => [o.id, true])),
          hydrated: true,
        });
      } catch {
        // Избранное не пришло — сердечки просто будут пустыми. Ронять экран
        // выдачи из-за этого нельзя.
      } finally {
        hydration = null;
      }
    })();
    return hydration;
  },

  // Оптимистично: сохранение — жест, а не транзакция, и ждать сети, прежде чем
  // закрасить сердечко, значит показывать пользователю ложное «не сохранилось».
  // Отказ откатывает состояние обратно.
  toggleFavorite: async (objectId, chatId) => {
    const wasSaved = Boolean(get().saved[objectId]);
    set((s) => ({ saved: { ...s.saved, [objectId]: !wasSaved } }));
    try {
      if (wasSaved) {
        await removeFavorite(objectId);
        set((s) => ({
          saved: { ...s.saved, [objectId]: false },
          favorites: s.favorites.filter((f) => f.id !== objectId),
        }));
      } else {
        await addFavorite(objectId, chatId);
        // Карточку избранного собирает бэк (адрес, обложка, saved_at), поэтому
        // не выдумываем её здесь — перечитываем список.
        const page = await listFavorites(100, 0);
        set({
          favorites: page.objects,
          saved: Object.fromEntries(page.objects.map((o) => [o.id, true])),
        });
      }
    } catch {
      set((s) => ({ saved: { ...s.saved, [objectId]: wasSaved } }));
    }
  },

  rate: async (chatId, objectId, verdict, reason) => {
    const previous = get().verdicts[objectId];
    // Повторный клик по той же оценке снимает её на экране, но на бэке остаётся
    // прежней: ручка умеет только перезапись, не удаление. Поэтому повторный
    // клик отправляем как есть, а локально не мигаем.
    set((s) => ({ verdicts: { ...s.verdicts, [objectId]: verdict }, verdictsChatId: chatId }));
    try {
      await saveFeedback(chatId, objectId, verdict, reason);
    } catch {
      set((s) => {
        const next = { ...s.verdicts };
        if (previous) next[objectId] = previous;
        else delete next[objectId];
        return { verdicts: next };
      });
    }
  },

  resetVerdicts: (chatId) => set({ verdicts: {}, verdictsChatId: chatId }),
}));
