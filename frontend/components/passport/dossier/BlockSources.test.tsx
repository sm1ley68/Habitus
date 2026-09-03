import { render, screen } from "@testing-library/react";
import BlockSources, { ProxyBadge, worstKind } from "./BlockSources";
import type { BlockSource } from "@/lib/agent/types";

const proxy: BlockSource = {
  key: "noise", label: "Шум", kind: "proxy",
  basis: "модель по типам дорог", observed_at: "2026-04-10",
};
const computed: BlockSource = {
  key: "solar", label: "Инсоляция", kind: "computation",
  basis: "расчёт по геометрии зданий", observed_at: null,
};

test("худший уровень блока — прокси, даже если он один из трёх", () => {
  expect(worstKind([computed, proxy])).toBe("proxy");
});

test("плашка появляется только у блока с прокси", () => {
  render(<ProxyBadge sources={[computed, proxy]} />);
  expect(screen.getByText("оценка по модели")).toBeInTheDocument();
});

test("блок без прокси плашку не показывает — иначе помечено всё и не помечено ничто", () => {
  const { container } = render(<ProxyBadge sources={[computed]} />);
  expect(container).toBeEmptyDOMElement();
});

test("источник без даты рисуется без даты, а не с пустым местом", () => {
  render(<BlockSources sources={[computed]} />);
  expect(screen.getByText(/расчёт по геометрии зданий/)).toBeInTheDocument();
  expect(screen.queryByText("·", { exact: false })).not.toBeInTheDocument();
});

test("пустой список источников не рисует ничего", () => {
  const { container } = render(<BlockSources sources={[]} />);
  expect(container).toBeEmptyDOMElement();
});
