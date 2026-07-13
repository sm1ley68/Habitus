import { render, screen } from "@testing-library/react";
import { it, expect } from "vitest";
import NoiseViz from "./NoiseViz";

it("labels the dB value", () => {
  render(<NoiseViz metrics={{ db: 35 }} />);
  expect(screen.getByTestId("noise")).toBeInTheDocument();
  expect(screen.getByText(/35 дБ/)).toBeInTheDocument();
});
