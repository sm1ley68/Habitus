import { render, screen, act } from "@testing-library/react";
import { test, expect, beforeEach } from "vitest";
import EmptyResult from "./EmptyResult";
import { useSession } from "@/lib/store/session";
import { runResult } from "@/test/fixtures";

beforeEach(() => useSession.getState().reset());

test("называет условие, обнулившее выборку", () => {
  // Пустой экран без объяснения читается как поломка продукта. Диагностика
  // приходит из ML именно для того, чтобы назвать виновника.
  act(() => useSession.getState().finish(runResult({
    diagnostics: [
      { constraint: "база", remaining: 340 },
      { constraint: "цена", remaining: 0 },
      { constraint: "комнаты", remaining: 0 },
    ],
  })));

  render(<EmptyResult />);

  expect(screen.getByText(/условие «цена»/)).toBeInTheDocument();
  expect(screen.getByText(/до него подходило 340/)).toBeInTheDocument();
});

test("без диагностики показывает общий совет, а не выдумывает причину", () => {
  act(() => useSession.getState().finish(runResult({ diagnostics: [] })));

  render(<EmptyResult />);

  expect(screen.getByText(/Попробуйте ослабить условия/)).toBeInTheDocument();
  expect(screen.queryByText(/обнулило условие/)).not.toBeInTheDocument();
});

test("не назначает виновным первый же шаг", () => {
  // Если ноль стоит на самом первом шаге, «предыдущего» нет — назвать
  // виновника нечем, и выдумывать его нельзя.
  act(() => useSession.getState().finish(runResult({
    diagnostics: [{ constraint: "база", remaining: 0 }],
  })));

  render(<EmptyResult />);

  expect(screen.queryByText(/обнулило условие/)).not.toBeInTheDocument();
  expect(screen.getByText(/Попробуйте ослабить условия/)).toBeInTheDocument();
});
