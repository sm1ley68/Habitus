import { render, screen, act, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LayerToggles from "./LayerToggles";
import { useSession } from "@/lib/store/session";

beforeEach(() => act(() => useSession.getState().reset()));

test("clicking a layer toggles it in the store", async () => {
  render(<LayerToggles />);
  await userEvent.click(screen.getByRole("button", { name: "Шум" }));
  expect(useSession.getState().activeLayers.noise).toBe(true);
});

test("renders a Russian label per layer and toggles typed ids", () => {
  render(<LayerToggles />);
  const schools = screen.getByRole("button", { name: "Школы" });
  expect(schools).toHaveAttribute("aria-pressed", "true");
  fireEvent.click(schools);
  expect(useSession.getState().activeLayers.schools).toBe(false);
});
