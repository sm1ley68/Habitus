import { it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import MapPreviewCard from "./MapPreviewCard";

const BASE = {
  id: "cian_1",
  name: "2-комн, 40 м²",
  address: "Москва, Снежная улица, 4",
  cover_image: "https://cdn/a.jpg",
  price_from: 21_300_000,
};

it("показывает адрес, цену и обложку", () => {
  render(<MapPreviewCard data={BASE} anchor={{ x: 10, y: 20 }} onOpen={() => {}} />);
  expect(screen.getByText("Москва, Снежная улица, 4")).toBeInTheDocument();
  expect(screen.getByText(/21.3 млн/)).toBeInTheDocument();
  expect((screen.getByRole("img") as HTMLImageElement).src).toBe("https://cdn/a.jpg");
});

it("без адреса ведёт синтезированным именем", () => {
  render(<MapPreviewCard data={{ ...BASE, address: "" }} anchor={{ x: 0, y: 0 }} onOpen={() => {}} />);
  expect(screen.getByText("2-комн, 40 м²")).toBeInTheDocument();
});

it("не рисует процент совпадения, когда его нет — вне подбора он не существует", () => {
  render(<MapPreviewCard data={BASE} anchor={{ x: 0, y: 0 }} onOpen={() => {}} />);
  expect(screen.queryByLabelText(/совпадение/)).toBeNull();
});

it("рисует процент совпадения для объекта из выдачи", () => {
  render(
    <MapPreviewCard data={{ ...BASE, match_score: 87 }} anchor={{ x: 0, y: 0 }} onOpen={() => {}} />,
  );
  expect(screen.getByLabelText("87% совпадение")).toBeInTheDocument();
});

it("клик по карточке ведёт в паспорт", () => {
  const onOpen = vi.fn();
  render(<MapPreviewCard data={BASE} anchor={{ x: 0, y: 0 }} onOpen={onOpen} />);
  fireEvent.click(screen.getByRole("button"));
  expect(onOpen).toHaveBeenCalledOnce();
});

it("Esc закрывает превью", () => {
  const onClose = vi.fn();
  render(
    <MapPreviewCard data={BASE} anchor={{ x: 0, y: 0 }} onOpen={() => {}} onClose={onClose} />,
  );
  fireEvent.keyDown(screen.getByRole("button"), { key: "Escape" });
  expect(onClose).toHaveBeenCalledOnce();
});
