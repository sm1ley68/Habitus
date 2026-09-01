import { describe, it, expect } from "vitest";
import { diffKeys, featureKey, keyById } from "./sync";

const point = (lng: number, lat: number, props: Record<string, unknown> = {}): GeoJSON.Feature =>
  ({ type: "Feature", properties: props, geometry: { type: "Point", coordinates: [lng, lat] } });

describe("featureKey", () => {
  // Ключ обязан быть стабильным между двумя ответами бэка на пересекающиеся
  // вьюпорты — иначе диф увидит «всё новое» и пересоберёт слой целиком.
  it("is stable for the same point across responses", () => {
    expect(featureKey(point(37.6, 55.75, { name: "Тверская" })))
      .toBe(featureKey(point(37.6, 55.75, { name: "Тверская" })));
  });

  it("separates different points and different names", () => {
    expect(featureKey(point(37.6, 55.75))).not.toBe(featureKey(point(37.7, 55.75)));
    expect(featureKey(point(37.6, 55.75, { name: "А" })))
      .not.toBe(featureKey(point(37.6, 55.75, { name: "Б" })));
  });

  it("prefers an explicit id when the feature carries one", () => {
    const withId = { ...point(37.6, 55.75), properties: { id: "cian_1" } };
    const moved = { ...point(30.3, 59.9), properties: { id: "cian_1" } };
    expect(featureKey(withId)).toBe(featureKey(moved));
  });

  it("keys lines by their route identity, not by every coordinate", () => {
    const line = (coords: [number, number][]): GeoJSON.Feature =>
      ({ type: "Feature", properties: { ref: "3", system: "subway" },
         geometry: { type: "LineString", coordinates: coords } });
    expect(featureKey(line([[37.5, 55.7], [37.6, 55.8]])))
      .toBe(featureKey(line([[37.5, 55.7], [37.6, 55.8]])));
    expect(featureKey(line([[37.5, 55.7], [37.6, 55.8]])))
      .not.toBe(featureKey(line([[30.3, 59.9], [30.4, 60.0]])));
  });
});

describe("diffKeys", () => {
  it("reports only what entered and left", () => {
    expect(diffKeys(["a", "b", "c"], ["b", "c", "d"]))
      .toEqual({ added: ["d"], removed: ["a"] });
  });

  it("is empty for an unchanged set — панорамирование по тем же данным", () => {
    expect(diffKeys(["a", "b"], ["b", "a"])).toEqual({ added: [], removed: [] });
  });
});

describe("keyById", () => {
  it("indexes listing features by their external id", () => {
    const features = [point(37.6, 55.7, { id: "cian_1" }), point(37.7, 55.8, { id: "cian_2" })];
    expect([...keyById(features).keys()]).toEqual(["cian_1", "cian_2"]);
  });

  it("drops features without an id — рисовать их всё равно нечем", () => {
    expect(keyById([point(37.6, 55.7)]).size).toBe(0);
  });
});
