import { describe, it, expect } from "vitest";
import { minimumPointZoom, shouldFetchLayer, zoomStyleKey } from "./layers";

describe("shouldFetchLayer", () => {
  // Слой из одних точек ниже своего порога не рисуется вообще — качать его
  // мегабайты, чтобы спрятать стилем, незачем.
  it("skips point-only layers below their zoom threshold", () => {
    expect(shouldFetchLayer("bars", 12)).toBe(false);
    expect(shouldFetchLayer("bars", 14)).toBe(true);
    expect(shouldFetchLayer("schools", 11.9)).toBe(false);
    expect(shouldFetchLayer("schools", 12)).toBe(true);
    expect(shouldFetchLayer("parks", 10)).toBe(false);
  });

  // Метро — не только точки: линии метро/МЦК/МЦД видны на любом зуме, поэтому
  // порог точек не должен отменять запрос всего слоя.
  it("always fetches layers that carry lines or polygons", () => {
    expect(shouldFetchLayer("metro", 8)).toBe(true);
    expect(shouldFetchLayer("noise", 8)).toBe(true);
    expect(shouldFetchLayer("crime", 8)).toBe(true);
  });

  it("fetches everything while the zoom is still unknown", () => {
    expect(shouldFetchLayer("bars", null)).toBe(true);
  });
});

describe("zoomStyleKey", () => {
  // Перестиливание слоя — проход по всем фичам. Внутри одной полосы зума стиль
  // не меняется, значит и вызывать setStyle не за чем.
  it("stays stable inside one zoom band and flips across a break", () => {
    expect(zoomStyleKey("schools", 12.1)).toBe(zoomStyleKey("schools", 13.9));
    expect(zoomStyleKey("schools", 13.9)).not.toBe(zoomStyleKey("schools", 14.2));
    expect(zoomStyleKey("metro", 14.0)).not.toBe(zoomStyleKey("metro", 14.6));
  });

  // У crime прозрачность — непрерывная функция зума (densityOpacity), поэтому
  // полосами её замораживать нельзя: ключ обязан меняться внутри полосы.
  it("keeps the crime opacity gradient alive between breaks", () => {
    expect(zoomStyleKey("crime", 12.0)).not.toBe(zoomStyleKey("crime", 13.0));
  });
});

describe("minimumPointZoom", () => {
  it("mirrors the thresholds the map styles points with", () => {
    expect(minimumPointZoom("metro")).toBe(10.5);
    expect(minimumPointZoom("schools")).toBe(12);
    expect(minimumPointZoom("bars")).toBe(14);
    expect(minimumPointZoom("parks")).toBe(12);
  });
});
