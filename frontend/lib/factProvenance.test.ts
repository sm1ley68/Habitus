import { factProvenance } from "./factProvenance";

describe("factProvenance", () => {
  it("пешие минуты — замер по графу улиц", () => {
    expect(factProvenance("2 минуты до школы")).toBe("measured");
    expect(factProvenance("1 минута до метро")).toBe("measured");
  });

  it("плотность баров — замер по справочнику заведений", () => {
    expect(factProvenance("баров рядом: 7")).toBe("measured");
  });

  it("неизвестный тег метки не получает — фронт не имеет права её выдумывать", () => {
    expect(factProvenance("евроремонт")).toBeNull();
  });
});
