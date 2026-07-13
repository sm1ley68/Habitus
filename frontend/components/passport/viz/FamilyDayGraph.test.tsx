import { render, screen } from "@testing-library/react";
import { it, expect } from "vitest";
import FamilyDayGraph from "./FamilyDayGraph";
import { LIFESTYLE_BLOCKS } from "@/lib/data/mock";

const block = LIFESTYLE_BLOCKS.find((b) => b.key === "family_routing")!;

it("renders a day lane per household member", () => {
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
  const data = block.data as { members: { label: string }[] };
  for (const m of data.members) {
    expect(screen.getByText(m.label)).toBeInTheDocument();
  }
});
