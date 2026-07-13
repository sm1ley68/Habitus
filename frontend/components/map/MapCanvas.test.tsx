import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import MapCanvas, { createPinElement } from "./MapCanvas";

vi.mock("@/lib/map/useMaplibre", () => ({
  useMaplibre: () => ({ map: null, ready: false, missingKey: true }),
}));

describe("MapCanvas", () => {
  it("renders a graceful placeholder when the map key is missing", () => {
    const { getByTestId } = render(<MapCanvas />);
    expect(getByTestId("map-missing-key")).toBeInTheDocument();
  });
});

describe("createPinElement", () => {
  it("carries its property id and marks the top match", () => {
    const el = createPinElement(
      { id: "jk-neva-residence", match_score: 96 } as never,
      true,
    );
    expect(el.dataset.pinId).toBe("jk-neva-residence");
    expect(el.className).toContain("pin");
    expect(el.dataset.top).toBe("true");
    expect(el.getAttribute("role")).toBe("button");
    expect(el.getAttribute("aria-label")).toContain("96");
  });

  it("marks a non-top pin and exposes its stagger index", () => {
    const el = createPinElement(
      { id: "jk-rechnoy-kvartal", match_score: 78 } as never,
      false,
      3,
    );
    expect(el.dataset.top).toBe("false");
    expect(el.className).not.toContain("pin--top");
    expect(el.style.getPropertyValue("--pin-index")).toBe("3");
  });
});
