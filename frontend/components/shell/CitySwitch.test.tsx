import { render, screen, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import CitySwitch from "./CitySwitch";
import { useSession } from "@/lib/store/session";

beforeEach(() => act(() => useSession.getState().reset()));

test("Москва идёт первой — она единственный наполненный город", () => {
  render(<CitySwitch />);
  const [first] = screen.getAllByRole("button");
  expect(first).toHaveTextContent("Москва");
});

test("Санкт-Петербург выключен, пока по нему нет данных", () => {
  render(<CitySwitch />);
  expect(screen.getByRole("button", { name: /Санкт-Петербург/ })).toBeDisabled();
});

test("рядом с выключенным городом стоит причина, а не молчание", () => {
  render(<CitySwitch />);
  expect(screen.getByText("данные готовим")).toBeInTheDocument();
});

test("клик по выключенному городу не переключает сессию", async () => {
  render(<CitySwitch />);
  await userEvent.click(screen.getByRole("button", { name: /Санкт-Петербург/ }));
  expect(useSession.getState().city).toBe("msk");
});
