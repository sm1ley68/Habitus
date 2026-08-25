import { describe, it, expect } from "vitest";
import { LAYER_COLORS, layerColor, layerPaintColor } from "./style";

// mapStyleUrl отсюда ушёл вместе с MapLibre: у Google Maps стиль задаётся
// mapId в облаке, а не URL стиля. Осталась палитра слоёв — её и проверяем.
describe("палитра слоёв", () => {
  it("у каждого слоя свой цвет, кроме намеренно совпадающих баров и их скоплений", () => {
    const distinct = new Set(Object.values(LAYER_COLORS));
    expect(distinct.size).toBe(Object.keys(LAYER_COLORS).length - 1);
    expect(LAYER_COLORS.crime).toBe(LAYER_COLORS.bars);
  });

  it("ни один слой не красится в барвинковый акцент наших объектов", () => {
    // #6f7cc8 помечает НАШИ объекты; городская точка того же цвета читалась бы
    // как результат поиска.
    expect(Object.values(LAYER_COLORS).map((c) => c.toLowerCase())).not.toContain("#6f7cc8");
  });

  it("layerColor отдаёт цвет слоя", () => {
    expect(layerColor("parks")).toBe(LAYER_COLORS.parks);
  });

  it("фолбэк по геометрии разводит точки, линии и полигоны", () => {
    const byGeometry = ["Point", "LineString", "Polygon"].map(layerPaintColor);
    expect(new Set(byGeometry).size).toBe(3);
  });
});
