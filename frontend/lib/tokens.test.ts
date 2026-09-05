import { PALETTE } from "./tokens";

it("палитра не содержит индиго дефолтного комплекта", () => {
  const values = Object.values(PALETTE).map((v) => v.toLowerCase());
  expect(values).not.toContain("#6f7cc8");
  expect(values).not.toContain("#7c8cff");
});

it("цвета происхождения заданы и различимы", () => {
  expect(PALETTE.evidence).toBe("#14493C");
  expect(PALETTE.estimate).toBe("#9A7534");
  expect(PALETTE.compromise).toBe("#A65A43");
  const set = new Set([PALETTE.evidence, PALETTE.estimate, PALETTE.compromise]);
  expect(set.size).toBe(3);
});
