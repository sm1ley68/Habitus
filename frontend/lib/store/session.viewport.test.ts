import { vi, describe, it, expect, beforeEach } from "vitest";

const fetchLayers = vi.fn(async () => ({}));
const fetchListings = vi.fn(async () => ({ type: "FeatureCollection", features: [] }));
vi.mock("@/lib/api/geo", () => ({
  fetchLayers: (...args: unknown[]) => fetchLayers(...(args as [])),
  fetchListings: (...args: unknown[]) => fetchListings(...(args as [])),
}));

const { useSession } = await import("./session");

const requestedLayers = () => fetchLayers.mock.calls.at(-1)?.[1] as unknown as string[];

beforeEach(() => {
  useSession.getState().reset();
  fetchLayers.mockClear();
  fetchListings.mockClear();
  useSession.setState({
    activeLayers: { communal: false, noise: false, schools: true, bars: true,
                    crime: true, parks: true, metro: true },
  });
});

describe("refreshViewport и зум", () => {
  // Точечные слои ниже своего порога не рисуются вообще — запрашивать их
  // мегабайты незачем. На вьюпорте центра это 1423 бара и 425 парков.
  it("не запрашивает точечные слои ниже их порога зума", async () => {
    await useSession.getState().refreshViewport([37.5, 55.7, 37.7, 55.8], 11);
    expect(requestedLayers()).toEqual(["crime", "metro"]);
  });

  it("запрашивает их, когда карта доехала до нужного зума", async () => {
    await useSession.getState().refreshViewport([37.5, 55.7, 37.7, 55.8], 14);
    expect(requestedLayers().sort()).toEqual(["bars", "crime", "metro", "parks", "schools"]);
  });

  it("без известного зума ведёт себя как раньше — тянет все активные слои", async () => {
    await useSession.getState().refreshViewport([37.5, 55.7, 37.7, 55.8]);
    expect(requestedLayers().sort()).toEqual(["bars", "crime", "metro", "parks", "schools"]);
  });

  it("запоминает зум, чтобы им пользовались и другие загрузки", async () => {
    await useSession.getState().refreshViewport([37.5, 55.7, 37.7, 55.8], 13);
    expect(useSession.getState().zoom).toBe(13);
    fetchLayers.mockClear();
    await useSession.getState().loadLayer("bars");
    expect(fetchLayers).not.toHaveBeenCalled();
  });
});
