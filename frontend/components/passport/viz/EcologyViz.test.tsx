import { render, screen } from "@testing-library/react";
import { it, expect } from "vitest";
import EcologyViz from "./EcologyViz";

it("shows green-space and industrial-distance figures", () => {
  render(<EcologyViz metrics={{ greenSpaces: 2, industrialKm: 2 }} />);
  expect(screen.getByTestId("ecology")).toBeInTheDocument();
  expect(screen.getByText(/2 км/)).toBeInTheDocument();
});
