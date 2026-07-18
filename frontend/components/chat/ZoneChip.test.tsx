import { render, screen } from "@testing-library/react";
import ZoneChip from "./ZoneChip";

test("рисует лейбл зоны", () => {
  render(<ZoneChip label="центр (ЦАО)" />);
  expect(screen.getByText("центр (ЦАО)")).toBeInTheDocument();
});

test("ничего не рисует без лейбла", () => {
  const { container } = render(<ZoneChip label={null} />);
  expect(container).toBeEmptyDOMElement();
});
