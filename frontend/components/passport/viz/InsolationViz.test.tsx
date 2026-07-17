import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import InsolationViz from "./InsolationViz";
import { LIFESTYLE_BLOCKS } from "@/test/fixtures";

it("renders the day arc and the direct-light window label", () => {
  render(<InsolationViz metrics={{ orientationDeg: 225, directLightFrom: 14, directLightTo: 18, db: 35 }} />);
  expect(screen.getByTestId("insolation")).toBeInTheDocument();
  expect(screen.getByText(/14:00–18:00/)).toBeInTheDocument();
});

it("shows the compass bearing when orientation is provided", () => {
  render(<InsolationViz metrics={{ orientationDeg: 225, directLightFrom: 14, directLightTo: 18 }} />);
  expect(screen.getByText(/окна на ЮЗ/)).toBeInTheDocument();
});

const block = LIFESTYLE_BLOCKS.find((b) => b.key === "view_and_climate")!;

it("renders season controls and a view-type badge from data", () => {
  render(<InsolationViz metrics={block.metrics ?? {}} data={block.data} />);
  expect(screen.getByRole("button", { name: /зима/i })).toBeInTheDocument();
  expect(screen.getByText(/двор|парк/i)).toBeInTheDocument();
});
