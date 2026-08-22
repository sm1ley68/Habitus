import { render, screen, act } from "@testing-library/react";
import PropertyList from "./PropertyList";
import { useSession } from "@/lib/store/session";
import { PROPERTIES, runResult } from "@/test/fixtures";

test("renders one card per property with match scores", () => {
  act(() =>
    useSession.getState().finish(runResult({ properties: PROPERTIES })),
  );
  render(<PropertyList />);
  // карточка ведёт адресом, имя — фолбэк (см. PropertyCard.test)
  expect(screen.getByText(PROPERTIES[0].address)).toBeInTheDocument();
  expect(screen.getByLabelText("96% совпадение")).toBeInTheDocument();
  expect(screen.getAllByRole("button").length).toBe(PROPERTIES.length);
});
