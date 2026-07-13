import { describe, it, expect } from "vitest";
import { DUR, EASE, SPRING } from "./motion";

describe("motion tokens", () => {
  it("exposes durations in seconds ascending", () => {
    expect(DUR.instant).toBeLessThan(DUR.fast);
    expect(DUR.fast).toBeLessThan(DUR.base);
    expect(DUR.base).toBeLessThan(DUR.slow);
    expect(DUR.slow).toBeLessThan(DUR.cinematic);
  });
  it("easings are 4-number cubic-bezier arrays", () => {
    for (const e of Object.values(EASE)) {
      expect(e).toHaveLength(4);
      e.forEach((n) => expect(typeof n).toBe("number"));
    }
  });
  it("springs are framer-motion spring configs", () => {
    for (const s of Object.values(SPRING)) {
      expect(s.type).toBe("spring");
      expect(s.stiffness).toBeGreaterThan(0);
      expect(s.damping).toBeGreaterThan(0);
    }
  });
});
