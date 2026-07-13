import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import PropertyCard from "./PropertyCard";
import { PROPERTIES } from "@/lib/data/mock";
import { useSession } from "@/lib/store/session";

describe("PropertyCard", () => {
  it("shows cover, name, price and match score, and hover sets hovered id", () => {
    const onOpen = vi.fn();
    render(<PropertyCard property={PROPERTIES[0]} index={0} onOpen={onOpen} />);
    expect(screen.getByRole("img", { name: /Neva Residence/i })).toBeInTheDocument();
    expect(screen.getByText(/18.5 млн/i)).toBeInTheDocument();
    expect(screen.getByLabelText("96% совпадение")).toBeInTheDocument();
    fireEvent.mouseEnter(screen.getByRole("button"));
    expect(useSession.getState().hoveredId).toBe("jk-neva-residence");
    fireEvent.mouseLeave(screen.getByRole("button"));
    expect(useSession.getState().hoveredId).toBe(null);
    fireEvent.click(screen.getByRole("button"));
    expect(onOpen).toHaveBeenCalledWith(0);
  });
});
