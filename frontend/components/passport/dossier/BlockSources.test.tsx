import { render, screen } from "@testing-library/react";
import BlockSources, { ProxyBadge, worstKind } from "./BlockSources";
import Chapter from "./Chapter";
import SecondaryGrid from "./SecondaryGrid";
import type { BlockSource, LifestyleBlock } from "@/lib/agent/types";

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
  const { container } = render(<BlockSources sources={[computed]} />);
  const li = container.querySelector("li");
  expect(li?.textContent).toBe("Инсоляция — вычисление, расчёт по геометрии зданий");
});

test("пустой список источников не рисует ничего", () => {
  const { container } = render(<BlockSources sources={[]} />);
  expect(container).toBeEmptyDOMElement();
});

test("глава hero-блока показывает источники и плашку", () => {
  const block: LifestyleBlock = {
    key: "unknown_layer", title: "Вид и климат", icon: "sun", score: "B",
    description: "Описание", sources: [proxy],
  };
  render(<Chapter block={block} index={0} />);
  expect(screen.getByText("оценка по модели")).toBeInTheDocument();
  expect(screen.getByText(/модель по типам дорог/)).toBeInTheDocument();
});

test("карточка вторичного блока показывает источники и плашку", () => {
  const block: LifestyleBlock = {
    key: "unknown_layer", title: "Вид и климат", icon: "sun", score: "B",
    description: "Описание", sources: [proxy],
  };
  render(<SecondaryGrid blocks={[block]} />);
  expect(screen.getByText("оценка по модели")).toBeInTheDocument();
  expect(screen.getByText(/модель по типам дорог/)).toBeInTheDocument();
});
