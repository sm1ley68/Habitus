import { describe, it, expect, vi } from "vitest";
import {
  POI_GLYPHS, drawGlyph, poiIconImageId, registerPoiIcons, renderGlyph,
  type GlyphContext, type GlyphOp,
} from "./icons";
import { LAYER_COLORS } from "./style";
import { MAP_LAYER_IDS } from "@/lib/agent/types";

// Заглушка вместо canvas: в jsdom его нет, а проверять надо ЧТО рисуется,
// а не как выглядит растр.
function fakeContext() {
  const calls: string[] = [];
  const ctx: GlyphContext = {
    fillStyle: "", font: "", textAlign: "", textBaseline: "",
    beginPath: () => calls.push("beginPath"),
    closePath: () => calls.push("closePath"),
    moveTo: (x, y) => calls.push(`moveTo(${x},${y})`),
    lineTo: (x, y) => calls.push(`lineTo(${x},${y})`),
    arc: (x, y, r) => calls.push(`arc(${x},${y},${r})`),
    rect: (x, y, w, h) => calls.push(`rect(${x},${y},${w},${h})`),
    fill: () => calls.push("fill"),
    fillText: (t, x, y) => calls.push(`fillText(${t},${x},${y})`),
  };
  return { ctx, calls };
}

describe("значки точечных слоёв", () => {
  it("каждый точечный слой имеет свой глиф", () => {
    for (const id of ["parks", "schools", "bars", "metro"] as const) {
      expect(POI_GLYPHS[id], `нет глифа для ${id}`).toBeTruthy();
      expect(POI_GLYPHS[id]!.length).toBeGreaterThan(0);
    }
  });

  it("глифы держатся внутри квадрата 32×32 — иначе значок обрежется", () => {
    for (const [id, ops] of Object.entries(POI_GLYPHS)) {
      for (const op of ops as GlyphOp[]) {
        const coords: number[] =
          op.op === "circle" ? [op.x - op.r, op.x + op.r, op.y - op.r, op.y + op.r]
          : op.op === "rect" ? [op.x, op.x + op.w, op.y, op.y + op.h]
          : op.op === "poly" ? op.points.flat()
          : [op.x, op.y];
        for (const c of coords) {
          expect(c, `${id}: координата ${c} вне холста`).toBeGreaterThanOrEqual(0);
          expect(c, `${id}: координата ${c} вне холста`).toBeLessThanOrEqual(32);
        }
      }
    }
  });

  it("рисует дерево кроной-треугольником и стволом", () => {
    // Круглая крона на тонкой ножке проверку глазами не прошла: на 10 пикселях
    // она читается как лампочка. Крона обязана быть треугольной.
    const { ctx, calls } = fakeContext();
    drawGlyph(ctx, POI_GLYPHS.parks!);
    expect(calls.filter((c) => c.startsWith("lineTo(")).length).toBe(2); // треугольник
    expect(calls.some((c) => c.startsWith("arc("))).toBe(false);
    expect(calls.some((c) => c.startsWith("rect("))).toBe(true);        // ствол
    expect(calls.filter((c) => c === "fill").length).toBe(2);
  });

  it("рисует букву М для метро", () => {
    const { ctx, calls } = fakeContext();
    drawGlyph(ctx, POI_GLYPHS.metro!);
    expect(calls.some((c) => c.startsWith("fillText(М"))).toBe(true);
  });

  it("глиф белый — он ложится поверх цветного кружка слоя", () => {
    const { ctx } = fakeContext();
    drawGlyph(ctx, POI_GLYPHS.bars!);
    expect(ctx.fillStyle).toBe("#ffffff");
  });

  it("многоугольник замыкается, иначе заливка вытечет", () => {
    const { ctx, calls } = fakeContext();
    drawGlyph(ctx, [{ op: "poly", points: [[1, 1], [5, 1], [3, 5]] }]);
    expect(calls).toEqual([
      "beginPath", "moveTo(1,1)", "lineTo(5,1)", "lineTo(3,5)", "closePath", "fill",
    ]);
  });
});

describe("регистрация значков на карте", () => {
  it("без canvas значков нет, но карта не падает", () => {
    // jsdom не реализует getContext — renderGlyph обязан вернуть null.
    expect(renderGlyph("parks")).toBeNull();
    const map = { hasImage: () => false, addImage: vi.fn() };
    expect(() => registerPoiIcons(map)).not.toThrow();
    expect(map.addImage).not.toHaveBeenCalled();
  });

  it("уже зарегистрированный значок не перезаписывается", () => {
    const map = { hasImage: () => true, addImage: vi.fn() };
    registerPoiIcons(map);
    expect(map.addImage).not.toHaveBeenCalled();
  });

  it("идентификаторы картинок не пересекаются между слоями", () => {
    const ids = MAP_LAYER_IDS.map(poiIconImageId);
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe("цвета слоёв", () => {
  it("у каждого тумблера свой цвет", () => {
    const colors = MAP_LAYER_IDS.map((id) => LAYER_COLORS[id]);
    expect(colors.every(Boolean)).toBe(true);
  });

  it("точечные слои различимы между собой — это и была исходная проблема", () => {
    const points = ["parks", "schools", "bars", "metro"] as const;
    const colors = points.map((id) => LAYER_COLORS[id]);
    expect(new Set(colors).size).toBe(points.length);
  });

  it("ни один слой не красится в барвинковый акцент наших объектов", () => {
    // --accent: #6f7cc8 — им помечены объекты подбора, городские точки не имеют
    // права на него претендовать.
    expect(Object.values(LAYER_COLORS)).not.toContain("#6f7cc8");
  });

  it("скопления баров совпадают по цвету с самими барами", () => {
    expect(LAYER_COLORS.crime).toBe(LAYER_COLORS.bars);
  });
});
