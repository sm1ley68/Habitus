import { render, screen } from "@testing-library/react";
import Provenance, { provenanceOfSource, provenanceOfLeg } from "./Provenance";

describe("Provenance", () => {
  it("источник-наблюдение — подтверждённая величина", () => {
    expect(provenanceOfSource("observation")).toBe("measured");
    expect(provenanceOfSource("computation")).toBe("measured");
    expect(provenanceOfSource("proxy")).toBe("estimate");
  });

  it("плечо по прямой — оценка, по сети — замер", () => {
    expect(provenanceOfLeg({ estimate_kind: "straight_line" })).toBe("estimate");
    expect(provenanceOfLeg({ estimate_kind: "model" })).toBe("estimate");
    expect(provenanceOfLeg({})).toBe("measured");
  });

  it("метка подписана словом, а не только цветом", () => {
    render(<Provenance kind="estimate" />);
    expect(screen.getByText("оценка")).toBeInTheDocument();
  });
});
