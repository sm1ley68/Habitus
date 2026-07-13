import { render, screen, act } from "@testing-library/react";
import ResultScreen from "./ResultScreen";
import { useSession } from "@/lib/store/session";

beforeEach(() => act(() => useSession.getState().reset()));

test("empty properties renders EmptyResult", () => {
  act(() => useSession.setState({ screen: "result", properties: [] }));
  render(<ResultScreen />);
  expect(screen.getByText("В этой зоне пока нет точных совпадений")).toBeInTheDocument();
});
