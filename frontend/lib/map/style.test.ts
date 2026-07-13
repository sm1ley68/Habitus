import { describe, it, expect, afterEach } from "vitest";
import { mapStyleUrl } from "./style";

const orig = process.env.NEXT_PUBLIC_MAPTILER_KEY;
afterEach(() => { process.env.NEXT_PUBLIC_MAPTILER_KEY = orig; });

describe("mapStyleUrl", () => {
  it("returns a maptiler style url when key present", () => {
    process.env.NEXT_PUBLIC_MAPTILER_KEY = "abc123";
    expect(mapStyleUrl()).toContain("api.maptiler.com");
    expect(mapStyleUrl()).toContain("abc123");
  });
  it("returns null when key absent", () => {
    delete process.env.NEXT_PUBLIC_MAPTILER_KEY;
    expect(mapStyleUrl()).toBeNull();
  });
});
