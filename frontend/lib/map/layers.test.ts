import { describe, it, expect } from "vitest";
import {
  ICON_FADE_END, ICON_FADE_START, FALLBACK_FONT, hasGlyphs, iconLayerId,
  iconLayout, iconPaint, metroLabelLayerId, metroLabelLayout, metroLabelPaint,
  pickLabelFont, pointCirclePaint, densityFillOpacity,
} from "./layers";
import { LAYER_COLORS } from "./style";
import { poiIconImageId } from "./icons";

describe("кружки точечных слоёв", () => {
  it("красятся в цвет своего слоя, а не в общий синий", () => {
    expect(pointCirclePaint("parks")["circle-color"]).toBe(LAYER_COLORS.parks);
    expect(pointCirclePaint("bars")["circle-color"]).toBe(LAYER_COLORS.bars);
    expect(pointCirclePaint("parks")["circle-color"])
      .not.toBe(pointCirclePaint("bars")["circle-color"]);
  });

  it("радиус растёт с приближением", () => {
    const expr = pointCirclePaint("metro")["circle-radius"] as unknown[];
    expect(expr[0]).toBe("interpolate");
    // ["interpolate", ["linear"], ["zoom"], zoom1, r1, zoom2, r2, …]
    const stops = expr.slice(3).map(Number);
    const zooms = stops.filter((_, i) => i % 2 === 0);
    const radii = stops.filter((_, i) => i % 2 === 1);
    expect(radii.length).toBeGreaterThan(1);
    expect(zooms).toEqual([...zooms].sort((a, b) => a - b));
    expect(radii).toEqual([...radii].sort((a, b) => a - b));
  });

  it("появляется прозрачным — иначе кроссфейд превращается в скачок", () => {
    expect(pointCirclePaint("schools")["circle-opacity"]).toBe(0);
    expect(pointCirclePaint("schools")["circle-stroke-opacity"]).toBe(0);
  });
});

describe("значки", () => {
  it("слой значков не конфликтует по id со слоем кружков", () => {
    expect(iconLayerId("parks")).not.toBe("layer-parks");
  });

  it("ссылается на картинку своего слоя", () => {
    expect(iconLayout("bars")["icon-image"]).toBe(poiIconImageId("bars"));
  });

  it("не прячется при столкновении — дырявый слой хуже плотного", () => {
    expect(iconLayout("schools")["icon-allow-overlap"]).toBe(true);
  });

  it("проявляется только при приближении", () => {
    const o = iconPaint()["icon-opacity"] as unknown[];
    expect(o).toEqual(expect.arrayContaining([ICON_FADE_START, 0, ICON_FADE_END, 1]));
    expect(ICON_FADE_START).toBeLessThan(ICON_FADE_END);
  });
});

describe("подписи станций метро", () => {
  it("берут название из свойств точки", () => {
    expect(metroLabelLayout(FALLBACK_FONT)["text-field"]).toEqual(["get", "name"]);
  });

  it("прячутся при наложении: 276 станций иначе слипнутся", () => {
    const l = metroLabelLayout(FALLBACK_FONT);
    expect(l["text-allow-overlap"]).toBe(false);
    expect(l["text-optional"]).toBe(true);
  });

  it("имеют белую обводку — иначе текст теряется на подложке", () => {
    expect(metroLabelPaint()["text-halo-color"]).toBe("#ffffff");
  });

  it("живут отдельным слоем от значков", () => {
    expect(metroLabelLayerId()).not.toBe(iconLayerId("metro"));
  });
});

describe("шрифт подписей", () => {
  it("берётся из самого стиля карты — выдуманное начертание даёт пустые подписи", () => {
    const style = {
      glyphs: "https://example/{fontstack}/{range}.pbf",
      layers: [
        { type: "background" },
        { type: "symbol", layout: { "text-font": ["Noto Sans Medium"] } },
      ],
    };
    expect(pickLabelFont(style)).toEqual(["Noto Sans Medium"]);
  });

  it("падает на запасное начертание, когда в стиле подписей нет", () => {
    expect(pickLabelFont({ layers: [{ type: "background" }] })).toEqual(FALLBACK_FONT);
    expect(pickLabelFont(null)).toEqual(FALLBACK_FONT);
  });

  it("стиль без шрифтов — подписей не будет, но карта жива", () => {
    expect(hasGlyphs(null)).toBe(false);
    expect(hasGlyphs({})).toBe(false);
    expect(hasGlyphs({ glyphs: "" })).toBe(false);
    expect(hasGlyphs({ glyphs: "https://example/{fontstack}/{range}.pbf" })).toBe(true);
  });
});

describe("заливка скоплений баров", () => {
  it("гаснет при приближении — проверка глазами показала сплошное пятно у земли", () => {
    const expr = densityFillOpacity() as unknown[];
    const stops = expr.slice(3).map(Number);
    const opacities = stops.filter((_, i) => i % 2 === 1);
    expect(opacities[0]).toBeGreaterThan(0);
    expect(opacities[opacities.length - 1]).toBe(0);
    expect(opacities).toEqual([...opacities].sort((a, b) => b - a));
  });

  it("исчезает ровно там, где проявляются значки заведений", () => {
    const expr = densityFillOpacity() as unknown[];
    const stops = expr.slice(3).map(Number);
    const zoomWhereZero = stops[stops.indexOf(0, 1) - 1];
    expect(zoomWhereZero).toBe(ICON_FADE_END);
  });
});
