import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import MapCanvas, { createPinElement, collectZonePositions } from "./MapCanvas";
import type { GeoZone } from "@/lib/agent/types";

vi.mock("@/lib/map/useMaplibre", () => ({
  useMaplibre: () => ({ map: null, ready: false, missingKey: true }),
}));

describe("MapCanvas", () => {
  it("renders a graceful placeholder when the map key is missing", () => {
    const { getByTestId } = render(<MapCanvas />);
    expect(getByTestId("map-missing-key")).toBeInTheDocument();
  });
});

describe("collectZonePositions", () => {
  const zone = (features: unknown[]): GeoZone =>
    ({ type: "FeatureCollection", features }) as GeoZone;

  // Регрессия: бэк шлёт FeatureCollection с features: [] когда в запросе не было
  // области и ничего не нашлось. Раньше карта падала на features[0].geometry.
  it("returns nothing for an empty feature collection", () => {
    expect(collectZonePositions(zone([]))).toEqual([]);
  });

  it("returns nothing for a missing zone or a feature without geometry", () => {
    expect(collectZonePositions(null)).toEqual([]);
    expect(collectZonePositions(undefined)).toEqual([]);
    expect(collectZonePositions(zone([{ type: "Feature", properties: {} }]))).toEqual([]);
  });

  it("flattens a Polygon ring", () => {
    const fc = zone([
      {
        type: "Feature",
        properties: {},
        geometry: {
          type: "Polygon",
          coordinates: [[[37.6, 55.7], [37.7, 55.7], [37.7, 55.8], [37.6, 55.7]]],
        },
      },
    ]);
    expect(collectZonePositions(fc)).toEqual([
      [37.6, 55.7], [37.7, 55.7], [37.7, 55.8], [37.6, 55.7],
    ]);
  });

  it("flattens every polygon of a MultiPolygon across all features", () => {
    const fc = zone([
      {
        type: "Feature",
        properties: {},
        geometry: {
          type: "MultiPolygon",
          coordinates: [
            [[[37.5, 55.6], [37.6, 55.6], [37.6, 55.7], [37.5, 55.6]]],
            [[[37.8, 55.9], [37.9, 55.9], [37.9, 56.0], [37.8, 55.9]]],
          ],
        },
      },
      {
        type: "Feature",
        properties: {},
        geometry: {
          type: "Polygon",
          coordinates: [[[38.0, 56.1], [38.1, 56.1], [38.1, 56.2], [38.0, 56.1]]],
        },
      },
    ]);
    const positions = collectZonePositions(fc);
    expect(positions).toHaveLength(12);
    expect(positions).toContainEqual([37.9, 56.0]);
    expect(positions).toContainEqual([38.0, 56.1]);
  });
});

describe("createPinElement", () => {
  it("carries its property id and marks the top match", () => {
    const el = createPinElement(
      { id: "jk-neva-residence", match_score: 96 } as never,
      true,
    );
    expect(el.dataset.pinId).toBe("jk-neva-residence");
    expect(el.className).toContain("pin");
    expect(el.dataset.top).toBe("true");
    expect(el.getAttribute("role")).toBe("button");
    expect(el.getAttribute("aria-label")).toContain("96");
  });

  it("marks a non-top pin and exposes its stagger index", () => {
    const el = createPinElement(
      { id: "jk-rechnoy-kvartal", match_score: 78 } as never,
      false,
      3,
    );
    expect(el.dataset.top).toBe("false");
    expect(el.className).not.toContain("pin--top");
    expect(el.style.getPropertyValue("--pin-index")).toBe("3");
  });
});
