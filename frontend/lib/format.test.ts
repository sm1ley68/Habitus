import { money } from "./format";

test("formats millions of rubles", () => {
  expect(money(18500000)).toBe("18.5 млн ₽");
  expect(money(21000000)).toBe("21 млн ₽");
});
