import { render, screen } from "@testing-library/react";
import RelaxationCard from "./RelaxationCard";

it("метит ослабление условий как компромисс — единственное место, где этот вид происхождения виден", () => {
  render(<RelaxationCard />);
  expect(screen.getByText("компромисс")).toBeInTheDocument();
});
