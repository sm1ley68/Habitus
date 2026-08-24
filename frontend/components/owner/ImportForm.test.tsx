import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import type { ImportPreview, OwnerListing } from "@/lib/agent/owner";
import { OwnerApiError } from "@/lib/api/owner";
import ImportForm from "./ImportForm";

const previewImport = vi.fn();
const importListing = vi.fn();
const push = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    previewImport: (...args: unknown[]) => previewImport(...args),
    importListing: (...args: unknown[]) => importListing(...args),
  };
});

vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));

function draft(over: Partial<OwnerListing> = {}): OwnerListing {
  return {
    id: "1", external_id: "cian_318394906", origin: "cian", status: "draft",
    verification: "unverified", city: "msk", price: 12500000, area: 54.3,
    kitchen_area: null, rooms: 2, level: 4, levels: 17,
    address: "Москва, улица Мельникова, 3к1", coordinates: [37.6595, 55.7108],
    window_orientation: [], description: "", photos: [],
    source_url: "https://www.cian.ru/sale/flat/318394906/", import_error: "",
    published_at: null, updated_at: "2026-08-23T10:00:00Z", ...over,
  };
}

function preview(over: Partial<ImportPreview> = {}): ImportPreview {
  return { verdict: "new", draft: draft(), similar: [], ...over };
}

async function submit(url = "https://www.cian.ru/sale/flat/318394906/") {
  await userEvent.type(screen.getByLabelText(/ссылк/i), url);
  await userEvent.click(screen.getByRole("button", { name: /проверить|найти/i }));
}

beforeEach(() => {
  previewImport.mockReset();
  importListing.mockReset();
  push.mockReset();
});

test("вердикт new предлагает импортировать", async () => {
  previewImport.mockResolvedValue(preview());
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/Мельникова/)).toBeInTheDocument());
  expect(screen.getByRole("button", { name: /импортировать/i })).toBeInTheDocument();
});

test("вердикт claimable предлагает забрать уже известный объект", async () => {
  previewImport.mockResolvedValue(preview({ verdict: "claimable" }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/уже знаем эту квартиру/i)).toBeInTheDocument());
  expect(screen.getByRole("button", { name: /это моя квартира/i })).toBeInTheDocument();
});

test("вердикт already_yours ведёт на существующую карточку", async () => {
  previewImport.mockResolvedValue(preview({ verdict: "already_yours", existing_id: "42" }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/уже в вашем кабинете/i)).toBeInTheDocument());
  await userEvent.click(screen.getByRole("link", { name: /открыть/i }));
  expect(screen.getByRole("link", { name: /открыть/i })).toHaveAttribute("href", "/lk/listings/42");
});

test("похожий объект показывается предупреждением и не блокирует импорт", async () => {
  previewImport.mockResolvedValue(preview({
    similar: [{ external_id: "cian_777", address: "Мельникова 3к1", price: 12000000, area: 54 }],
  }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/похоже/i));
  expect(screen.getByRole("button", { name: /импортировать/i })).toBeEnabled();
});

test("недоступность Циана объясняется и уводит в ручную форму", async () => {
  previewImport.mockRejectedValue(new OwnerApiError("cian_unavailable", "Циан сейчас не отдаёт данные"));
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByText(/не отдаёт данные/i)).toBeInTheDocument());
  expect(screen.getByRole("link", { name: /заполнить вручную/i })).toHaveAttribute(
    "href",
    expect.stringContaining("/lk/new"),
  );
});

test("чужое объявление объясняется, а не падает молча", async () => {
  previewImport.mockRejectedValue(
    new OwnerApiError("listing_claimed_by_other", "Это объявление уже привязано к другому аккаунту"),
  );
  render(<ImportForm />);
  await submit();

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/другому аккаунту/i));
  expect(screen.queryByRole("button", { name: /импортировать/i })).toBeNull();
});

test("кривая ссылка ловится до запроса", async () => {
  previewImport.mockRejectedValue(new OwnerApiError("cian_url_invalid", "Это не похоже на ссылку"));
  render(<ImportForm />);
  await submit("моя квартира");

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/не похоже на ссылку/i));
});

test("успешный импорт уводит на карточку", async () => {
  previewImport.mockResolvedValue(preview());
  importListing.mockResolvedValue(draft({ id: "99", status: "published" }));
  render(<ImportForm />);
  await submit();

  await waitFor(() => screen.getByRole("button", { name: /импортировать/i }));
  await userEvent.click(screen.getByRole("button", { name: /импортировать/i }));

  await waitFor(() => expect(push).toHaveBeenCalledWith("/lk/listings/99"));
});
