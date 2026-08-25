import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import CreateWizard from "./CreateWizard";

const createListing = vi.fn();
const updateListing = vi.fn();
const push = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    createListing: (...a: unknown[]) => createListing(...a),
    updateListing: (...a: unknown[]) => updateListing(...a),
  };
});
vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));
// Карта требует WebGL, которого в jsdom нет: подменяем на кнопку, ставящую точку.
vi.mock("./PinMap", () => ({
  default: ({ onPick }: { onPick: (c: [number, number]) => void }) => (
    <button type="button" onClick={() => onPick([37.6055, 55.7601])}>
      поставить точку
    </button>
  ),
}));

beforeEach(() => {
  createListing.mockReset().mockResolvedValue({ id: "77", photos: [], status: "draft" });
  updateListing.mockReset().mockResolvedValue({ id: "77", photos: [], status: "draft" });
  push.mockReset();
});

test("без точки на карте дальше не пускает", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  expect(screen.getByRole("alert")).toHaveTextContent(/точк/i);
  expect(createListing).not.toHaveBeenCalled();
});

test("первый переход создаёт черновик, чтобы работа не терялась", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /поставить точку/i }));
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  await waitFor(() => expect(createListing).toHaveBeenCalledTimes(1));
  const draft = createListing.mock.calls[0][0];
  // Контракт координат единый по всему проекту: [lng, lat].
  expect(draft.coordinates).toEqual([37.6055, 55.7601]);
  expect(draft.city).toBe("msk");
});

test("шаги идут в понятном порядке и назад возвращает", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /поставить точку/i }));
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  await waitFor(() => expect(screen.getByText(/шаг 2/i)).toBeInTheDocument());
  await userEvent.click(screen.getByRole("button", { name: /назад/i }));
  expect(screen.getByText(/шаг 1/i)).toBeInTheDocument();
});

test("город определяется по поставленной точке, а не спрашивается", async () => {
  render(<CreateWizard />);
  await userEvent.click(screen.getByRole("button", { name: /поставить точку/i }));
  await userEvent.click(screen.getByRole("button", { name: /далее/i }));

  await waitFor(() => expect(createListing).toHaveBeenCalled());
  expect(screen.queryByLabelText(/выберите город/i)).toBeNull();
});
