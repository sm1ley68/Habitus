import { it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import PhotoGallery from "./PhotoGallery";

const PHOTOS = [
  "https://cdn.cian.site/a.jpg",
  "https://cdn.cian.site/b.jpg",
  "https://cdn.cian.site/c.jpg",
];

it("рисует миниатюру на каждый снимок", () => {
  render(<PhotoGallery images={PHOTOS} alt="Снежная улица, 4" />);
  const thumbs = screen.getAllByRole("button", { name: /Открыть фото/ });
  expect(thumbs).toHaveLength(3);
});

it("без снимков не рисует ничего — пустой рамки быть не должно", () => {
  const { container } = render(<PhotoGallery images={[]} alt="дом" />);
  expect(container).toBeEmptyDOMElement();
});

it("заглушку за снимок не считает", () => {
  const { container } = render(
    <PhotoGallery images={["/static/placeholder-cover.svg"]} alt="дом" />,
  );
  expect(container).toBeEmptyDOMElement();
});

it("клик по миниатюре открывает лайтбокс на этом снимке", () => {
  render(<PhotoGallery images={PHOTOS} alt="дом" />);
  fireEvent.click(screen.getAllByRole("button", { name: /Открыть фото/ })[1]);
  const full = screen.getByTestId("lightbox-image") as HTMLImageElement;
  expect(full.src).toBe(PHOTOS[1]);
  expect(screen.getByText("2 / 3")).toBeInTheDocument();
});

it("стрелки листают и упираются в границы", () => {
  render(<PhotoGallery images={PHOTOS} alt="дом" />);
  fireEvent.click(screen.getAllByRole("button", { name: /Открыть фото/ })[0]);
  const img = () => screen.getByTestId("lightbox-image") as HTMLImageElement;

  fireEvent.click(screen.getByRole("button", { name: "Следующее фото" }));
  expect(img().src).toBe(PHOTOS[1]);

  // на первом снимке назад уже некуда — кнопка выключена
  fireEvent.click(screen.getByRole("button", { name: "Предыдущее фото" }));
  expect(img().src).toBe(PHOTOS[0]);
  expect(screen.getByRole("button", { name: "Предыдущее фото" })).toBeDisabled();
});

it("Esc закрывает лайтбокс", () => {
  render(<PhotoGallery images={PHOTOS} alt="дом" />);
  fireEvent.click(screen.getAllByRole("button", { name: /Открыть фото/ })[0]);
  expect(screen.getByTestId("lightbox-image")).toBeInTheDocument();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByTestId("lightbox-image")).toBeNull();
});

it("миниатюры грузятся лениво — 22 снимка иначе тянут мегабайты при открытии", () => {
  render(<PhotoGallery images={PHOTOS} alt="дом" />);
  for (const img of screen.getAllByRole("img")) {
    expect(img).toHaveAttribute("loading", "lazy");
  }
});
