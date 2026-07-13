import { render, screen } from "@testing-library/react";
import SocialXrayViz from "./SocialXrayViz";
import { LIFESTYLE_BLOCKS } from "@/lib/data/mock";

const block = LIFESTYLE_BLOCKS.find((b) => b.key === "social_environment")!;

test("renders three risk meters and layer toggles", () => {
  render(<SocialXrayViz metrics={block.metrics ?? {}} data={block.data} />);
  expect(screen.getAllByRole("meter")).toHaveLength(3);
  expect(screen.getByRole("button", { name: /коммунал/i })).toBeInTheDocument();
});
