import { render, screen, act } from "@testing-library/react";
import SearchWaiting, { isThinking } from "./SearchWaiting";
import { useSession } from "@/lib/store/session";

beforeEach(() => act(() => useSession.getState().reset()));

it("во время ожидания показывает просьбу человека", () => {
  act(() => useSession.setState({ stage: "geo" }));
  render(<SearchWaiting request="двушка до 25 млн, ребёнку в школу пешком" />);
  expect(screen.getByText(/ребёнку в школу пешком/)).toBeInTheDocument();
});

it("не изображает работу машины: никакого geo-engine", () => {
  act(() => useSession.setState({ stage: "geo" }));
  render(<SearchWaiting />);
  expect(screen.queryByText(/geo-engine/i)).not.toBeInTheDocument();
});

it("isThinking переехал без изменения поведения", () => {
  expect(isThinking("geo")).toBe(true);
  expect(isThinking("done")).toBe(false);
});
