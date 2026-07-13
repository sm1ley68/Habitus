import { render, screen } from "@testing-library/react";
import { test, expect } from "vitest";
import HonestFooter from "./HonestFooter";

test("hides sections that are empty", () => {
  const { container } = render(
    <HonestFooter compromises={[]} relaxation={[]} zone_rationale="" />,
  );
  expect(container).toBeEmptyDOMElement();
});

test("shows compromises when present", () => {
  render(
    <HonestFooter
      compromises={[{ block_key: "view_and_climate", text: "Компромисс по свету" }]}
      relaxation={[]}
      zone_rationale="Пересечение зон"
    />,
  );
  expect(screen.getByText(/компромисс по свету/i)).toBeInTheDocument();
});
