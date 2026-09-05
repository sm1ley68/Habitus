import { readFileSync } from "node:fs";
import { join } from "node:path";
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

// PALETTE — единственный источник правды, но globals.css не умеет его читать
// (CSS custom properties — не .ts) и держит две буквальные копии hex: в :root
// и в initial-value @property --glow-color. Без этого теста расхождение
// между PALETTE и CSS проходит молча — ни vitest, ни tsc его не ловят.
describe("globals.css синхронизирован с lib/tokens.ts (PALETTE)", () => {
  const cssPath = join(__dirname, "../app/globals.css");
  const css = readFileSync(cssPath, "utf-8");

  function extractBlock(selector: string): string {
    // [^]*? — как .*? с флагом s, но без него: не спотыкается на переносах строк
    // внутри блока (globals.css форматирован многострочно).
    const re = new RegExp(`${selector}\\s*\\{([^]*?)\\}`);
    const match = css.match(re);
    if (!match) {
      throw new Error(`В globals.css не найден блок ${selector}`);
    }
    return match[1];
  }

  function extractValue(block: string, property: string): string {
    const re = new RegExp(`(?:^|[\\s;{])${property}\\s*:\\s*([^;]+);`);
    const match = block.match(re);
    if (!match) {
      throw new Error(`В globals.css не найдено объявление ${property}`);
    }
    return match[1].trim();
  }

  const rootBlock = extractBlock(":root");

  const ROOT_VARS: Array<[cssVar: string, key: keyof typeof PALETTE]> = [
    ["--paper", "paper"],
    ["--ink", "ink"],
    ["--ink-muted", "inkMuted"],
    ["--ink-faint", "inkFaint"],
    ["--evidence", "evidence"],
    ["--estimate", "estimate"],
    ["--compromise", "compromise"],
  ];

  for (const [cssVar, key] of ROOT_VARS) {
    it(`:root ${cssVar} совпадает с PALETTE.${key}`, () => {
      const value = extractValue(rootBlock, cssVar);
      expect(value.toLowerCase()).toBe(PALETTE[key].toLowerCase());
    });
  }

  it("@property --glow-color: initial-value совпадает с PALETTE.evidence", () => {
    // var() в initial-value спекой @property запрещён (initial-value обязан
    // быть вычислимым без контекста), поэтому здесь неизбежен третий литерал —
    // и именно поэтому за ним нужен отдельный присмотр теста.
    const glowColorBlock = extractBlock("@property --glow-color");
    const value = extractValue(glowColorBlock, "initial-value");
    expect(value.toLowerCase()).toBe(PALETTE.evidence.toLowerCase());
  });
});
