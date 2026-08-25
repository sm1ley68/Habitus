import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import type { OwnerListing } from "@/lib/agent/owner";
import ListingEditor from "./ListingEditor";

const updateListing = vi.fn();
const publishListing = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    updateListing: (...a: unknown[]) => updateListing(...a),
    publishListing: (...a: unknown[]) => publishListing(...a),
  };
});
vi.mock("./PinMap", () => ({ default: () => <div /> }));

function listing(over: Partial<OwnerListing> = {}): OwnerListing {
  return {
    id: "1", external_id: "cian_1", origin: "cian", status: "published",
    verification: "unverified", city: "msk", price: 12500000, area: 54.3,
    kitchen_area: null, rooms: 2, level: 4, levels: 17,
    address: "Москва, улица Мельникова, 3к1", coordinates: [37.6595, 55.7108],
    window_orientation: [], description: "Тихая двушка", photos: [],
    source_url: "", import_error: "", published_at: "2026-08-23T10:00:00Z",
    updated_at: "2026-08-23T10:00:00Z", ...over,
  };
}

beforeEach(() => {
  updateListing.mockReset().mockImplementation(async (_id, patch) => listing(patch));
  publishListing.mockReset().mockResolvedValue(listing());
});

test("правка цены уходит на бэк", async () => {
  render(<ListingEditor listing={listing()} />);
  const price = screen.getByLabelText(/цена/i);
  await userEvent.clear(price);
  await userEvent.type(price, "11000000");
  await userEvent.click(screen.getByRole("button", { name: /сохранить/i }));

  await waitFor(() =>
    expect(updateListing).toHaveBeenCalledWith("1", expect.objectContaining({ price: 11000000 })),
  );
});

test("непроверенное объявление честно помечено", () => {
  render(<ListingEditor listing={listing()} />);
  expect(screen.getByText(/не подтверждено/i)).toBeInTheDocument();
});

test("превью показывает то же, что увидит покупатель", () => {
  render(<ListingEditor listing={listing()} />);
  const preview = screen.getByTestId("listing-preview");
  expect(preview).toHaveTextContent("Мельникова");
  expect(preview).toHaveTextContent("54,3");
});

test("пустое поле сохраняется как отсутствующее, а не как ноль", async () => {
  render(<ListingEditor listing={listing({ kitchen_area: null })} />);
  await userEvent.click(screen.getByRole("button", { name: /сохранить/i }));

  await waitFor(() => expect(updateListing).toHaveBeenCalled());
  const patch = updateListing.mock.calls[0][1];
  expect(patch.kitchen_area === null || patch.kitchen_area === undefined).toBe(true);
});
