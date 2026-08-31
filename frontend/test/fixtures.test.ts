// Раннер (не только tsc) на инвариант из habitus/online/schema.py:
// MetroRide.wait_min — остаток округления, а не независимый замер. Без этого
// теста фикстуру проверял только tsc (структура полей), но не то, что числа
// внутри неё реально складываются в total_minutes.
import { describe, expect, it } from "vitest";
import { metroRideFixture } from "./fixtures";

describe("metroRideFixture", () => {
  it("satisfies segments + transfers + both walks + wait_min === total_minutes", () => {
    const segmentsSum = metroRideFixture.segments.reduce((sum, s) => sum + s.minutes, 0);
    const transfersSum = metroRideFixture.transfers.reduce((sum, t) => sum + t.minutes, 0);
    const composed =
      metroRideFixture.walk_from_home_min +
      segmentsSum +
      transfersSum +
      metroRideFixture.walk_to_dest_min +
      metroRideFixture.wait_min;
    expect(composed).toBe(metroRideFixture.total_minutes);
  });
});
