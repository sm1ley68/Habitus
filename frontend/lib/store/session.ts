import { create } from "zustand";
import type {
  Stage, AgentEvent, Property, City, GeoZone, LayerId, ConstraintDiagnostic,
} from "@/lib/agent/types";
import { nextStage } from "@/lib/agent/stageMachine";
import type { AgentClient, RunResult } from "@/lib/agent/AgentClient";
import { fetchLayers, fetchListings, type LayerCollections } from "@/lib/api/geo";
import { fetchMoreResults } from "@/lib/api/results";
import type { StreamFailure } from "@/lib/api/streamError";

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
  /** area_label текущего поиска — человекочитаемая зона для чипа над выдачей. */
  areaLabel: string | null;
  hoveredId: string | null;
  /** Границы вьюпорта карты [minLon, minLat, maxLon, maxLat] — нужны evidence-слоям. */
  viewport: [number, number, number, number] | null;
  /** Все объявления под вьюпортом — чтобы открыть любое, а не только из выдачи. */
  mapListings: GeoJSON.FeatureCollection | null;
  /** Объект, открытый КЛИКОМ ПО КАРТЕ: он вне выдачи, поэтому лежит отдельно. */
  mapProperty: Property | null;
  /** chat_id текущего поиска — контекст для паспорта, чата по объекту и
   *  многоходового диалога: следующая реплика уходит в этот же чат. */
  chatId: string | null;
  /** Сколько объектов сохранено для текущего поиска целиком. */
  totalResults: number;
  /** Есть ли что дотянуть кнопкой «показать ещё». */
  hasMore: boolean;
  /** Идёт ли догрузка страницы прямо сейчас. */
  loadingMore: boolean;
  /** Почему выдача пуста — непустой только при нулевой выдаче. */
  diagnostics: ConstraintDiagnostic[];
  errorMessage: string | null;
  /** Код отказа — по нему ищут в логах шлюза и ML. */
  errorCode: string | null;
  /** Техническая улика: ручка ML, статус, стадия, тайминги. Null — улики нет. */
  errorCause: string | null;
  /** Что с этим делать. Null — сказать нечего, и выдумывать нельзя. */
  errorHint: string | null;
  _cancel?: () => void;

  startQuery: (client: AgentClient, query: string) => void;
  loadMore: () => Promise<void>;
  applyEvent: (e: AgentEvent) => void;
  finish: (result: RunResult) => void;
  fail: (failure: StreamFailure) => void;
  reset: () => void;
  setScreen: (s: Screen) => void;
  selectProperty: (i: number) => void;
  setCity: (c: City) => void;
  toggleHistory: () => void;
  toggleLayer: (id: LayerId) => void;
  loadLayer: (id: LayerId) => Promise<void>;
  setHoveredProperty: (id: string | null) => void;
  setViewport: (b: [number, number, number, number]) => void;
  loadMapListings: () => Promise<void>;
  openListingFromMap: (p: Property) => void;
}

const initial = {
  stage: "idle" as Stage,
  screen: "chat" as Screen,
  answer: "",
  properties: [] as Property[],
  selectedIndex: 0,
  city: "msk" as City,
  historyOpen: false,
  activeLayers: { communal: false, noise: false, schools: true, bars: false, crime: false, parks: true, metro: true } as Record<LayerId, boolean>,
  layerData: {} as LayerCollections,
  zoneGeoJSON: null as GeoZone | null,
  areaLabel: null as string | null,
  hoveredId: null as string | null,
  viewport: null as [number, number, number, number] | null,
  mapListings: null as GeoJSON.FeatureCollection | null,
  mapProperty: null as Property | null,
  chatId: null as string | null,
  totalResults: 0,
  hasMore: false,
  loadingMore: false,
  diagnostics: [] as ConstraintDiagnostic[],
  errorMessage: null as string | null,
  errorCode: null as string | null,
  errorCause: null as string | null,
  errorHint: null as string | null,
};

export const useSession = create<SessionState>((set, get) => ({
  ...initial,

  startQuery: (client, query) => {
    get()._cancel?.();
    // chatId НЕ сбрасываем: реплика уходит в тот же чат, и шлюз подмешает
    // разбор предыдущего шага. Диалог начинается заново только через
    // newDialog() или смену города.
    const chatId = get().chatId;
    set({ stage: "idle", answer: "", screen: "chat", properties: [],
          diagnostics: [], hasMore: false, totalResults: 0,
          errorMessage: null, errorCode: null, errorCause: null, errorHint: null });
    const cancel = client.run(query, {
      onEvent: (e) => get().applyEvent(e),
      onDone: (r) => get().finish(r),
      onError: (failure) => get().fail(failure),
    }, chatId ? { chatId } : undefined);
    set({ _cancel: cancel });
  },

  // «Показать ещё»: остаток сохранённого пула поднимается из
  // GET /chats/{id}/results, повторный поиск не запускается.
  loadMore: async () => {
    const { chatId, properties, hasMore, loadingMore } = get();
    if (!chatId || !hasMore || loadingMore) return;
    set({ loadingMore: true });
    try {
      const page = await fetchMoreResults(chatId, properties.length);
      set((s) => {
        const merged = [...s.properties, ...page.objects];
        return {
          properties: merged,
          totalResults: page.total,
          hasMore: merged.length < page.total && page.objects.length > 0,
          loadingMore: false,
        };
      });
    } catch {
      // Не дотянулось — оставляем то, что уже показано, и прячем кнопку:
      // повторный тык по неработающей кнопке хуже её отсутствия.
      set({ loadingMore: false, hasMore: false });
    }
  },

  applyEvent: (e) =>
    set((st) => ({
      stage: nextStage(st.stage, e),
      answer: e.token ? st.answer + e.token : st.answer,
    })),

  finish: ({ properties, zoneGeoJSON, areaLabel, chatId, total, hasMore, diagnostics }) =>
    set({ properties, stage: "done", screen: "result", zoneGeoJSON, areaLabel, chatId,
          totalResults: total, hasMore, diagnostics }),

  fail: (failure) => set({
    stage: "error",
    errorMessage: failure.message || null,
    errorCode: failure.code || null,
    errorCause: failure.cause ?? null,
    errorHint: failure.hint ?? null,
  }),

  // «Новый поиск» в LeftRail: обрывает и выдачу, и ДИАЛОГ — chatId уходит в
  // null, поэтому следующий запрос заведёт новый чат и уйдёт в ML без
  // prev_parsed. Это единственный способ начать разговор с чистого листа.
  reset: () => { get()._cancel?.(); set({ ...initial }); },

  // Уход с паспорта снимает объект, открытый с карты: иначе он перекрыл бы
  // обычный выбор из выдачи при следующем открытии.
  setScreen: (screen) => set(screen === "passport" ? { screen } : { screen, mapProperty: null }),
  selectProperty: (selectedIndex) => set({ selectedIndex, screen: "passport", mapProperty: null }),
  // Смена города обесценивает всё, что было посчитано для прежнего: слои,
  // выдачу и зону. Иначе на карте Питера остались бы московские полигоны.
  // chatId тоже сбрасывается: чат привязан к городу на бэке, продолжать
  // московский диалог питерской репликой нельзя.
  setCity: (city) => set({ city, layerData: {}, properties: [], zoneGeoJSON: null,
                           areaLabel: null, selectedIndex: 0, mapListings: null,
                           mapProperty: null, chatId: null, totalResults: 0,
                           hasMore: false, diagnostics: [] }),
  toggleHistory: () => set((s) => ({ historyOpen: !s.historyOpen })),

  // «Бары» — один тумблер на две формы одних и тех же данных: точки заведений
  // и буферы их скоплений (слой crime). Переключаются только вместе.
  toggleLayer: (id) => {
    const on = !get().activeLayers[id];
    const ids: LayerId[] = id === "bars" ? ["bars", "crime"] : [id];
    set((s) => ({
      activeLayers: {
        ...s.activeLayers,
        ...Object.fromEntries(ids.map((x) => [x, on])),
      },
    }));
    if (on) ids.forEach((x) => void get().loadLayer(x));
  },

  // Слой тянется под текущий вьюпорт. Повторные вкл/выкл по сети не бьют,
  // но смена города сбрасывает кэш (см. setCity).
  loadLayer: async (id) => {
    if (get().layerData[id]) return;
    try {
      const fetched = await fetchLayers(get().city, [id], get().viewport ?? undefined);
      set((s) => ({ layerData: { ...s.layerData, ...fetched } }));
    } catch {
      // Слой не пришёл — карта просто его не покажет. Молча, без падения.
    }
  },

  setHoveredProperty: (hoveredId) => set({ hoveredId }),
  setViewport: (viewport) => { set({ viewport }); void get().loadMapListings(); },

  // Точки объявлений перезапрашиваются под каждый новый вьюпорт: bbox — часть
  // запроса, кэшировать по городу нельзя.
  loadMapListings: async () => {
    const { city, viewport } = get();
    try {
      set({ mapListings: await fetchListings(city, viewport ?? undefined) });
    } catch {
      // Точек не будет — карта просто останется без слоя объявлений.
    }
  },

  openListingFromMap: (mapProperty) => set({ mapProperty, screen: "passport" }),
}));
