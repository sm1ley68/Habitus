import { render, screen } from "@testing-library/react";
import Provenance, { provenanceOfSource, provenanceOfLeg } from "./Provenance";

describe("Provenance", () => {
  describe("provenanceOfSource", () => {
    it("наблюдение и вычисление — замер", () => {
      expect(provenanceOfSource("observation")).toBe("measured");
      expect(provenanceOfSource("computation")).toBe("measured");
    });

    it("прокси-источник — модель", () => {
      expect(provenanceOfSource("proxy")).toBe("model");
    });
  });

  describe("provenanceOfLeg", () => {
    it("оценка по прямой — straight_line", () => {
      expect(provenanceOfLeg({ estimate_kind: "straight_line" })).toBe(
        "straight_line"
      );
    });

    it("оценка по модели — model", () => {
      expect(provenanceOfLeg({ estimate_kind: "model" })).toBe("model");
    });

    it("плечо без оценки — замер", () => {
      expect(provenanceOfLeg({})).toBe("measured");
    });
  });

  describe("Рендер с предложением смысла", () => {
    it("замер — замер", () => {
      render(<Provenance kind="measured" />);
      expect(screen.getByText("замер")).toBeInTheDocument();
    });

    it("прямая — по прямой", () => {
      render(<Provenance kind="straight_line" />);
      expect(screen.getByText("по прямой")).toBeInTheDocument();
    });

    it("модель — модель", () => {
      render(<Provenance kind="model" />);
      expect(screen.getByText("модель")).toBeInTheDocument();
    });

    it("компромисс — компромисс", () => {
      render(<Provenance kind="compromise" />);
      expect(screen.getByText("компромисс")).toBeInTheDocument();
    });
  });
});
