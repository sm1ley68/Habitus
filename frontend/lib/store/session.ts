import { create } from "zustand";
import type { Stage, AgentEvent, Property, City, GeoZone, LayerId } from "@/lib/agent/types";
import { nextStage } from "@/lib/agent/stageMachine";
import type { AgentClient } from "@/lib/agent/AgentClient";
import { ZONE_GEOJSON } from "@/lib/data/mock";

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
  zoneGeoJSON: GeoZone | null;
  hoveredId: string | null;
  _cancel?: () => void;

  startQuery: (client: AgentClient, query: string) => void;
  applyEvent: (e: AgentEvent) => void;
  finish: (properties: Property[]) => void;
  reset: () => void;
  setScreen: (s: Screen) => void;
  selectProperty: (i: number) => void;
  setCity: (c: City) => void;
  toggleHistory: () => void;
  toggleLayer: (id: LayerId) => void;
  setHoveredProperty: (id: string | null) => void;
}

const initial = {
  stage: "idle" as Stage,
  screen: "chat" as Screen,
  answer: "",
  properties: [] as Property[],
  selectedIndex: 0,
  city: "spb" as City,
  historyOpen: false,
  activeLayers: { communal: false, noise: false, schools: true, bars: false, ecology: false, parks: true } as Record<LayerId, boolean>,
  zoneGeoJSON: null as GeoZone | null,
  hoveredId: null as string | null,
};

export const useSession = create<SessionState>((set, get) => ({
  ...initial,

  startQuery: (client, query) => {
    get()._cancel?.();
    set({ stage: "idle", answer: "", screen: "chat", properties: [] });
    const cancel = client.run(query, {
      onEvent: (e) => get().applyEvent(e),
      onDone: (r) => get().finish(r.properties),
    });
    set({ _cancel: cancel });
  },

  applyEvent: (e) =>
    set((st) => ({
      stage: nextStage(st.stage, e),
      answer: e.token ? st.answer + e.token : st.answer,
    })),

  finish: (properties) => set({ properties, stage: "done", screen: "result", zoneGeoJSON: ZONE_GEOJSON }),

  reset: () => { get()._cancel?.(); set({ ...initial }); },

  setScreen: (screen) => set({ screen }),
  selectProperty: (selectedIndex) => set({ selectedIndex, screen: "passport" }),
  setCity: (city) => set({ city }),
  toggleHistory: () => set((s) => ({ historyOpen: !s.historyOpen })),
  toggleLayer: (id) =>
    set((s) => ({ activeLayers: { ...s.activeLayers, [id]: !s.activeLayers[id] } })),
  setHoveredProperty: (hoveredId) => set({ hoveredId }),
}));
