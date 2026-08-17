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

test("у каждого слоя свой цвет в легенде — четыре синих кружка и были проблемой", () => {
  const { container } = render(<LayerToggles />);
  act(() => {
    ["parks", "schools", "bars", "metro"].forEach((id) => {
      useSession.setState((s) => ({ activeLayers: { ...s.activeLayers, [id]: true } }));
    });
  });
  const swatches = Array.from(container.querySelectorAll("span[aria-hidden]"))
    .map((el) => (el as HTMLElement).style.backgroundColor)
    .filter((c) => c && c !== "transparent");
  expect(swatches.length).toBeGreaterThanOrEqual(4);
  expect(new Set(swatches).size).toBe(swatches.length);
});
