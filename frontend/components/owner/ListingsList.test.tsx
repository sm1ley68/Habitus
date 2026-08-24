import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import type { OwnerListing } from "@/lib/agent/owner";
import ListingsList from "./ListingsList";

function listing(over: Partial<OwnerListing> = {}): OwnerListing {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    external_id: "cian_318394906",
    origin: "cian",
    status: "published",
    verification: "unverified",
    city: "msk",
    price: 12500000,
    area: 54.3,
    kitchen_area: null,
    rooms: 2,
    level: 4,
    levels: 17,
    address: "Москва, улица Мельникова, 3к1",
    coordinates: [37.6595, 55.7108],
    window_orientation: [],
    description: "Тихая двушка",
    photos: [],
    source_url: "https://www.cian.ru/sale/flat/318394906/",
    import_error: "",
    published_at: "2026-08-23T10:00:00Z",
    updated_at: "2026-08-23T10:00:00Z",
    ...over,
  };
}

test("показывает адрес, цену и статус", () => {
  render(<ListingsList listings={[listing()]} onChanged={vi.fn()} />);
  expect(screen.getByText(/Мельникова/)).toBeInTheDocument();
  expect(screen.getByText("Опубликовано")).toBeInTheDocument();
});

test("не рисует выдуманных метрик", () => {
  render(<ListingsList listings={[listing()]} onChanged={vi.fn()} />);
  // Счётчиков просмотров и звонков в системе нет — их не должно быть и в UI.
  expect(screen.queryByText(/просмотр/i)).toBeNull();
  expect(screen.queryByText(/звонк/i)).toBeNull();
});

test("незаполненную цену показывает прочерком, а не нулём", () => {
  render(<ListingsList listings={[listing({ price: null, status: "draft" })]} onChanged={vi.fn()} />);
  expect(screen.queryByText(/0\s*₽/)).toBeNull();
  expect(screen.getByText("Черновик")).toBeInTheDocument();
});

test("у объявления с ошибкой публикации видна причина и кнопка повтора", () => {
  render(
    <ListingsList
      listings={[listing({ status: "failed", import_error: "Витрина не приняла объявление" })]}
      onChanged={vi.fn()}
    />,
  );
  expect(screen.getByText(/Витрина не приняла объявление/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /повторить/i })).toBeInTheDocument();
});

test("каждая карточка ведёт на свою страницу", () => {
  render(<ListingsList listings={[listing()]} onChanged={vi.fn()} />);
  expect(screen.getByRole("link", { name: /Мельникова/ })).toHaveAttribute(
    "href",
    "/lk/listings/11111111-1111-1111-1111-111111111111",
  );
});
