import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import FamilyDayGraph from "./FamilyDayGraph";
import SocialXrayViz from "./SocialXrayViz";
import { LIFESTYLE_BLOCKS } from "@/test/fixtures";

// Карты внутри блоков досье кадрируются по объекту: FamilyDayGraph — fitBounds
// по дому и маршрутам семьи, SocialXrayViz — по слоям вокруг дома. Хук по
// умолчанию cityAware, и тогда он по «idle» уводит камеру в центр города и
// сбрасывает зум — fitBounds не доживал до экрана, «карта семьи» показывала
// Кремль без маршрутов и без пина «Дом». Городская камера здесь запрещена.
const useGoogleMap = vi.fn(() => ({
  map: null, ready: false, missingKey: false, loadError: false, unavailable: false,
}));
vi.mock("@/lib/map/useGoogleMap", () => ({
  useGoogleMap: (...args: unknown[]) => useGoogleMap(...(args as [])),
}));

function optionsOf(): Record<string, unknown> {
  return useGoogleMap.mock.calls.at(-1)![1] as Record<string, unknown>;
}

describe("камера карт в досье принадлежит объекту, а не городу", () => {
  it("FamilyDayGraph не даёт хуку вернуть камеру в центр города", () => {
    const block = LIFESTYLE_BLOCKS.find((b) => b.key === "family_routing")!;
    render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
    expect(optionsOf().cityAware).toBe(false);
  });

  it("SocialXrayViz не даёт хуку вернуть камеру в центр города", () => {
    const block = LIFESTYLE_BLOCKS.find((b) => b.key === "social_environment")!;
    render(<SocialXrayViz metrics={block.metrics ?? {}} data={block.data} />);
    expect(optionsOf().cityAware).toBe(false);
  });
});
