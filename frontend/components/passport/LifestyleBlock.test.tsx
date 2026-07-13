import { it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import LifestyleBlock from "./LifestyleBlock";

it("unknown key falls back to text-only (no viz)", () => {
  render(<LifestyleBlock block={{ key: "mystery", title: "Загадка", icon: "route", score: "B", description: "Текст" }} />);
  expect(screen.getByText("Текст")).toBeInTheDocument();
  expect(screen.queryByTestId("viz")).toBeNull();
});

it("known key without metrics falls back to text-only", () => {
  render(<LifestyleBlock block={{ key: "ecology", title: "Экология", icon: "leaf", score: "A-", description: "Текст" }} />);
  expect(screen.getByText("Текст")).toBeInTheDocument();
  expect(screen.queryByTestId("viz")).toBeNull();
});

it("known key with metrics renders its viz above the text", () => {
  render(<LifestyleBlock block={{ key: "ecology", title: "Экология", icon: "leaf", score: "A-", description: "d", metrics: { greenSpaces: 2, industrialKm: 2 } }} />);
  expect(screen.getByTestId("viz")).toBeInTheDocument();
  expect(screen.getByText("d")).toBeInTheDocument();
});
