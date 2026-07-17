import { create } from "zustand";
import type { Stage, AgentEvent, Property, City, GeoZone, LayerId } from "@/lib/agent/types";
import { nextStage } from "@/lib/agent/stageMachine";
import type { AgentClient, RunResult } from "@/lib/agent/AgentClient";
import { fetchLayers, type LayerCollections } from "@/lib/api/geo";

export type Screen = "chat" | "result" | "map" | "passport";

interface SessionState {
  stage: Stage;
  screen: Screen;
  answer: string;
  properties: Property[];
  selectedIndex: number;
  city: City;
  historyOpen: boolean;
  activeLayers: Record<LayerId, boolean>;
  /** Слои, уже полученные с бэка. Ключ отсутствует → ещё не загружали. */
  layerData: LayerCollections;
  zoneGeoJSON: GeoZone | null;
  hoveredId: string | null;
  /** chat_id текущего поиска — контекст для паспорта и чата по объекту. */
  chatId: string | null;
  errorMessage: string | null;
  _cancel?: () => void;

  startQuery: (client: AgentClient, query: string) => void;
  applyEvent: (e: AgentEvent) => void;
  finish: (result: RunResult) => void;
  fail: (message: string) => void;
  reset: () => void;
  setScreen: (s: Screen) => void;
  selectProperty: (i: number) => void;
  setCity: (c: City) => void;
  toggleHistory: () => void;
  toggleLayer: (id: LayerId) => void;
  loadLayer: (id: LayerId) => Promise<void>;
  setHoveredProperty: (id: string | null) => void;
}

const initial = {
  stage: "idle" as Stage,
  screen: "chat" as Screen,
  answer: "",
  properties: [] as Property[],
  selectedIndex: 0,
  city: "msk" as City,
  historyOpen: false,
  activeLayers: { communal: false, noise: false, schools: true, bars: false, ecology: false, parks: true } as Record<LayerId, boolean>,
  layerData: {} as LayerCollections,
  zoneGeoJSON: null as GeoZone | null,
  hoveredId: null as string | null,
  chatId: null as string | null,
  errorMessage: null as string | null,
};

export const useSession = create<SessionState>((set, get) => ({
  ...initial,

  startQuery: (client, query) => {
    get()._cancel?.();
    set({ stage: "idle", answer: "", screen: "chat", properties: [], errorMessage: null });
    const cancel = client.run(query, {
      onEvent: (e) => get().applyEvent(e),
      onDone: (r) => get().finish(r),
      onError: (_code, message) => get().fail(message),
    });
    set({ _cancel: cancel });
  },

  applyEvent: (e) =>
    set((st) => ({
      stage: nextStage(st.stage, e),
      answer: e.token ? st.answer + e.token : st.answer,
    })),

  finish: ({ properties, zoneGeoJSON, chatId }) =>
    set({ properties, stage: "done", screen: "result", zoneGeoJSON, chatId }),

  fail: (errorMessage) => set({ stage: "error", errorMessage }),

  reset: () => { get()._cancel?.(); set({ ...initial }); },

  setScreen: (screen) => set({ screen }),
  selectProperty: (selectedIndex) => set({ selectedIndex, screen: "passport" }),
  setCity: (city) => set({ city }),
  toggleHistory: () => set((s) => ({ historyOpen: !s.historyOpen })),

  toggleLayer: (id) => {
    const on = !get().activeLayers[id];
    set((s) => ({ activeLayers: { ...s.activeLayers, [id]: on } }));
    if (on) void get().loadLayer(id);
  },

  // Слой тянется один раз и остаётся в кэше: повторные вкл/выкл не бьют по сети.
  loadLayer: async (id) => {
    if (get().layerData[id]) return;
    try {
      const fetched = await fetchLayers(get().city, [id]);
      set((s) => ({ layerData: { ...s.layerData, ...fetched } }));
    } catch {
      // Слой не пришёл — карта просто его не покажет. Молча, без падения.
    }
  },

  setHoveredProperty: (hoveredId) => set({ hoveredId }),
}));
