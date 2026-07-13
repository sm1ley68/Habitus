import { render, screen, act } from "@testing-library/react";
import AppShell from "./AppShell";
import { useSession } from "@/lib/store/session";

beforeEach(() => act(() => useSession.getState().reset()));

test("shows chat empty state by default", () => {
  render(<AppShell />);
  expect(screen.getByText("С чего начнём поиск?")).toBeInTheDocument();
});
test("routes to result screen when store screen is result", () => {
  act(() => useSession.setState({ screen: "result" }));
  render(<AppShell />);
  expect(screen.getByTestId("app-shell")).toBeInTheDocument();
});
