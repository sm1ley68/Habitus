import { render, screen, act } from "@testing-library/react";
import PropertyList from "./PropertyList";
import { useSession } from "@/lib/store/session";
import { PROPERTIES } from "@/test/fixtures";

test("renders one card per property with match scores", () => {
  act(() =>
    useSession.getState().finish({ properties: PROPERTIES, zoneGeoJSON: null, chatId: "c1" }),
  );
  render(<PropertyList />);
  expect(screen.getByText("ЖК Neva Residence")).toBeInTheDocument();
  expect(screen.getByLabelText("96% совпадение")).toBeInTheDocument();
  expect(screen.getAllByRole("button").length).toBe(PROPERTIES.length);
});
