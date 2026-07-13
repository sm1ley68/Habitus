import { render, screen } from "@testing-library/react";
import AppShell from "@/components/shell/AppShell";

test("all rail buttons have accessible names", () => {
  render(<AppShell />);
  for (const name of ["Новый поиск", "Результаты", "Карта", "История"]) {
    expect(screen.getByRole("button", { name })).toBeInTheDocument();
  }
});
test("composer input has an accessible label", () => {
  render(<AppShell />);
  expect(screen.getByLabelText("Запрос агенту")).toBeInTheDocument();
});
