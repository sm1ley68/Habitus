import { create } from "zustand";
import type {
  Stage, AgentEvent, Property, City, GeoZone, LayerId, ConstraintDiagnostic,
} from "@/lib/agent/types";
import { RENDERED_LAYER_IDS } from "@/lib/agent/types";
import { nextStage } from "@/lib/agent/stageMachine";
import type { AgentClient, RunResult } from "@/lib/agent/AgentClient";
import { fetchLayers, fetchListings, type LayerCollections } from "@/lib/api/geo";
import { fetchMoreResults } from "@/lib/api/results";
import type { StreamFailure } from "@/lib/api/streamError";
import { CITY_CENTER } from "@/lib/map/constants";
import { expandViewport, type Viewport } from "@/lib/map/viewport";

export type Screen = "chat" | "result" | "map" | "passport";

export interface SearchMessage {
  id: string;
  role: "user" | "assistant";
  text: string;
}

interface SessionState {
  stage: Stage;
  screen: Screen;
  answer: string;
  searchMessages: SearchMessage[];
  searchUpdating: boolean;
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
  /** Карта движется или получает данные для только что выбранной области. */
  mapUpdating: boolean;
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
  refineQuery: (client: AgentClient, query: string) => void;
  hydrateAllResults: (chatId: string) => Promise<void>;
  applyEvent: (e: AgentEvent) => void;
  finish: (result: RunResult) => void;
  finishRefinement: (result: RunResult) => void;
  fail: (failure: StreamFailure) => void;
  reset: () => void;
  setScreen: (s: Screen) => void;
  selectProperty: (i: number) => void;
  setCity: (c: City) => void;
  toggleHistory: () => void;
  toggleLayer: (id: LayerId) => void;
  loadLayer: (id: LayerId) => Promise<void>;
  setHoveredProperty: (id: string | null) => void;
  setMapUpdating: (value: boolean) => void;
  setViewport: (b: Viewport) => void;
  refreshViewport: (b: Viewport) => Promise<void>;
  loadMapListings: () => Promise<void>;
  openListingFromMap: (p: Property) => void;
}

const initial = {
  stage: "idle" as Stage,
  screen: "chat" as Screen,
  answer: "",
  searchMessages: [] as SearchMessage[],
  searchUpdating: false,
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
  mapUpdating: false,
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

let viewportController: AbortController | null = null;
let viewportRequestId = 0;
let resultsController: AbortController | null = null;
let resultsRequestId = 0;
let searchMessageSequence = 0;

function searchMessage(role: SearchMessage["role"], text: string): SearchMessage {
  searchMessageSequence += 1;
  return { id: `search-message-${searchMessageSequence}`, role, text };
}

function fallbackViewport(city: City): Viewport {
  const [lng, lat] = CITY_CENTER[city];
  return [lng - 0.12, lat - 0.075, lng + 0.12, lat + 0.075];
}

function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

function sameViewport(a: Viewport | null, b: Viewport | null) {
  return a === b || (!!a && !!b && a.every((value, index) => value === b[index]));
}

function cancelViewportRequest() {
  viewportRequestId += 1;
  viewportController?.abort();
  viewportController = null;
}

function cancelResultsRequest() {
  resultsRequestId += 1;
  resultsController?.abort();
  resultsController = null;
}

export const useSession = create<SessionState>((set, get) => ({
  ...initial,

  startQuery: (client, query) => {
    get()._cancel?.();
    cancelViewportRequest();
    cancelResultsRequest();
    // chatId НЕ сбрасываем: реплика уходит в тот же чат, и шлюз подмешает
    // разбор предыдущего шага. Диалог начинается заново только через
    // newDialog() или смену города.
    const chatId = get().chatId;
    const messages = chatId ? get().searchMessages : [];
    set({ stage: "idle", answer: "", screen: "chat", properties: [],
          diagnostics: [], hasMore: false, totalResults: 0,
          errorMessage: null, errorCode: null, errorCause: null, errorHint: null,
          layerData: {}, mapListings: null, viewport: null, mapUpdating: false,
          searchUpdating: false,
          searchMessages: [...messages, searchMessage("user", query)] });
    const cancel = client.run(query, {
      onEvent: (e) => get().applyEvent(e),
      onDone: (r) => get().finish(r),
      onError: (failure) => get().fail(failure),
    }, chatId ? { chatId } : undefined);
    set({ _cancel: cancel });
  },

  refineQuery: (client, query) => {
    const { chatId, searchMessages, searchUpdating, screen } = get();
    if (!chatId || searchUpdating) return;
    get()._cancel?.();
    cancelResultsRequest();
    set({
      stage: "idle",
      answer: "",
      errorMessage: null,
      errorCode: null,
      errorCause: null,
      errorHint: null,
      searchUpdating: true,
      screen: screen === "map" ? "map" : "result",
      searchMessages: [...searchMessages, searchMessage("user", query)],
    });
    const cancel = client.run(query, {
      onEvent: (event) => get().applyEvent(event),
      onDone: (result) => get().finishRefinement(result),
      onError: (failure) => set((state) => ({
        stage: "error",
        errorMessage: failure.message || null,
        errorCode: failure.code || null,
        errorCause: failure.cause ?? null,
        errorHint: failure.hint ?? null,
        searchUpdating: false,
        searchMessages: [
          ...state.searchMessages,
          searchMessage("assistant", failure.message || "Не удалось обновить подборку."),
        ],
      })),
    }, { chatId });
    set({ _cancel: cancel });
  },

  hydrateAllResults: async (chatId) => {
    const snapshot = get();
    if (snapshot.chatId !== chatId || !snapshot.hasMore) return;
    cancelResultsRequest();
    const requestId = resultsRequestId;
    const controller = new AbortController();
    resultsController = controller;
    set({ loadingMore: true });
    try {
      const page = await fetchMoreResults(
        chatId,
        snapshot.properties.length,
        50,
        controller.signal,
      );
      if (requestId !== resultsRequestId || get().chatId !== chatId) return;
      set((state) => {
        const knownIds = new Set(state.properties.map((property) => property.id));
        const additions = page.objects.filter((property) => !knownIds.has(property.id));
        const properties = [...state.properties, ...additions];
        return {
          properties,
          totalResults: page.total,
          hasMore: properties.length < page.total && page.objects.length > 0,
          loadingMore: false,
        };
      });
    } catch (error) {
      if (!isAbortError(error) && requestId === resultsRequestId) {
        set({ loadingMore: false });
      }
    } finally {
      if (requestId === resultsRequestId) resultsController = null;
    }
  },

  applyEvent: (e) =>
    set((st) => ({
      stage: nextStage(st.stage, e),
      answer: e.token ? st.answer + e.token : st.answer,
    })),

  finish: ({ properties, zoneGeoJSON, areaLabel, chatId, total, hasMore, diagnostics }) => {
    const response = get().answer.trim();
    set((state) => ({
      properties,
      stage: "done",
      screen: "result",
      zoneGeoJSON,
      areaLabel,
      chatId,
      totalResults: total,
      hasMore,
      diagnostics,
      searchUpdating: false,
      searchMessages: response
        ? [...state.searchMessages, searchMessage("assistant", response)]
        : state.searchMessages,
    }));
    if (chatId && hasMore) void get().hydrateAllResults(chatId);
  },

  finishRefinement: ({ properties, zoneGeoJSON, areaLabel, chatId, total, hasMore, diagnostics }) => {
    const response = get().answer.trim();
    set((state) => ({
      properties,
      zoneGeoJSON,
      areaLabel,
      chatId,
      totalResults: total,
      hasMore,
      diagnostics,
      viewport: null,
      stage: "done",
      searchUpdating: false,
      errorMessage: null,
      errorCode: null,
      errorCause: null,
      errorHint: null,
      searchMessages: response
        ? [...state.searchMessages, searchMessage("assistant", response)]
        : state.searchMessages,
    }));
    if (chatId && hasMore) void get().hydrateAllResults(chatId);
  },

  fail: (failure) => set({
    stage: "error",
    errorMessage: failure.message || null,
    errorCode: failure.code || null,
    errorCause: failure.cause ?? null,
    errorHint: failure.hint ?? null,
    searchUpdating: false,
  }),

  // «Новый поиск» в LeftRail: обрывает и выдачу, и ДИАЛОГ — chatId уходит в
  // null, поэтому следующий запрос заведёт новый чат и уйдёт в ML без
  // prev_parsed. Это единственный способ начать разговор с чистого листа.
  reset: () => {
    get()._cancel?.();
    cancelViewportRequest();
    cancelResultsRequest();
    set({ ...initial });
  },

  // Уход с паспорта снимает объект, открытый с карты: иначе он перекрыл бы
  // обычный выбор из выдачи при следующем открытии.
  setScreen: (screen) => set(screen === "passport" ? { screen } : { screen, mapProperty: null }),
  selectProperty: (selectedIndex) => set({ selectedIndex, screen: "passport", mapProperty: null }),
  // Смена города обесценивает всё, что было посчитано для прежнего: слои,
  // выдачу и зону. Иначе на карте Питера остались бы московские полигоны.
  // chatId тоже сбрасывается: чат привязан к городу на бэке, продолжать
  // московский диалог питерской репликой нельзя.
  setCity: (city) => {
    cancelViewportRequest();
    cancelResultsRequest();
    set({ city, layerData: {}, properties: [], zoneGeoJSON: null,
          areaLabel: null, selectedIndex: 0, mapListings: null,
          mapProperty: null, chatId: null, totalResults: 0,
          hasMore: false, diagnostics: [], viewport: null, mapUpdating: false,
          searchMessages: [], searchUpdating: false });
  },
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
    const viewport = get().viewport;
    if (viewport) void get().refreshViewport(viewport);
    else if (on) ids.forEach((x) => void get().loadLayer(x));
  },

  // До появления реального viewport берём небольшой фрагмент вокруг центра
  // города: экран размышления не должен заранее тянуть и держать весь город.
  loadLayer: async (id) => {
    try {
      const { city, viewport } = get();
      const fetched = await fetchLayers(city, [id], expandViewport(viewport ?? fallbackViewport(city)));
      const current = get();
      if (current.city !== city || !sameViewport(current.viewport, viewport)) return;
      set((s) => ({ layerData: { ...s.layerData, ...fetched } }));
    } catch {
      // Слой не пришёл — карта просто его не покажет. Молча, без падения.
    }
  },

  setHoveredProperty: (hoveredId) => set({ hoveredId }),
  setMapUpdating: (mapUpdating) => set({ mapUpdating }),
  setViewport: (viewport) => {
    void get().refreshViewport(viewport);
  },

  refreshViewport: async (viewport) => {
    cancelViewportRequest();
    const requestId = viewportRequestId;
    const controller = new AbortController();
    viewportController = controller;
    const { city, activeLayers } = get();
    const bufferedViewport = expandViewport(viewport);
    const layers = RENDERED_LAYER_IDS.filter((id) => activeLayers[id]);
    set({ mapUpdating: true });
    try {
      const [mapListings, layerData] = await Promise.all([
        fetchListings(city, bufferedViewport, controller.signal),
        fetchLayers(city, layers, bufferedViewport, controller.signal),
      ]);
      if (requestId !== viewportRequestId) return;
      set({ viewport, mapListings, layerData, mapUpdating: false });
    } catch (error) {
      if (!isAbortError(error) && requestId === viewportRequestId) {
        set({ mapUpdating: false });
      }
    } finally {
      if (requestId === viewportRequestId) viewportController = null;
    }
  },

  // Точки объявлений перезапрашиваются под каждый новый вьюпорт: bbox — часть
  // запроса, кэшировать по городу нельзя.
  loadMapListings: async () => {
    const { viewport } = get();
    if (viewport) await get().refreshViewport(viewport);
  },

  openListingFromMap: (mapProperty) => set({ mapProperty, screen: "passport" }),
}));
