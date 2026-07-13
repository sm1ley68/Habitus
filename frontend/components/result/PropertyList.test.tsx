import { render, screen, act } from "@testing-library/react";
import PropertyList from "./PropertyList";
import { useSession } from "@/lib/store/session";
import { PROPERTIES } from "@/lib/data/mock";

test("renders one card per property with match scores", () => {
  act(() => useSession.getState().finish(PROPERTIES));
  render(<PropertyList />);
  expect(screen.getByText("ЖК Neva Residence")).toBeInTheDocument();
  expect(screen.getByLabelText("96% совпадение")).toBeInTheDocument();
  expect(screen.getAllByRole("button").length).toBe(PROPERTIES.length);
});
