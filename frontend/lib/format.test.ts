import { money } from "./format";

test("formats millions of rubles", () => {
  expect(money(18500000)).toBe("18.5 млн ₽");
  expect(money(21000000)).toBe("21 млн ₽");
});

it("не выдумывает цену, когда её нет", () => {
  // Go шлёт *int64 и присылает null; раньше money(null) рисовал «0 млн ₽»
  expect(money(null)).toBe("цена не указана");
  expect(money(undefined)).toBe("цена не указана");
  expect(money(44872500)).toBe("44.9 млн ₽");
});
