import { describe, it, expect } from "vitest";
import { viewportChangedEnough, type Viewport } from "./viewport";

const box: Viewport = [37.5, 55.7, 37.7, 55.8];   // 0.2° × 0.1°

describe("viewportChangedEnough", () => {
  it("treats the first viewport as a change", () => {
    expect(viewportChangedEnough(null, box)).toBe(true);
  });

  // Микросдвиг после idle не должен запускать полный цикл «перекачать слои и
  // объявления»: это мегабайты на каждое дрожание карты.
  it("ignores a sub-threshold nudge", () => {
    expect(viewportChangedEnough(box, [37.501, 55.7005, 37.701, 55.8005])).toBe(false);
  });

  it("reports a real pan", () => {
    expect(viewportChangedEnough(box, [37.52, 55.7, 37.72, 55.8])).toBe(true);
  });

  it("reports a zoom change even when the centre stays put", () => {
    expect(viewportChangedEnough(box, [37.45, 55.675, 37.75, 55.825])).toBe(true);
  });
});
