import { render, screen } from "@testing-library/react";
import { it, expect } from "vitest";
import FamilyDayGraph from "./FamilyDayGraph";
import { LIFESTYLE_BLOCKS } from "@/test/fixtures";

const block = LIFESTYLE_BLOCKS.find((b) => b.key === "family_routing")!;

it("renders a day lane per household member", () => {
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
  const data = block.data as { members: { label: string }[] };
  for (const m of data.members) {
    expect(screen.getByText(m.label)).toBeInTheDocument();
  }
});

// Задача 17: нога с mode: "metro" и заполненным leg.metro разворачивает
// MetroRouteStrip под сжатой строкой Гантта.
it("разворачивает ленту метро под ногой с mode: metro и заполненным leg.metro", () => {
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
  // metroRideFixture: рельсовый отрезок Сокольники → Охотный Ряд, дальше
  // пересадка на Охотный Ряд → Белорусская — «Охотный Ряд» встречается дважды
  // (конец сегмента и начало пересадки), поэтому проверяем через getAllByText.
  expect(screen.getByText(/Сокольники/)).toBeInTheDocument();
  expect(screen.getAllByText(/Охотный Ряд/).length).toBeGreaterThan(0);
});
