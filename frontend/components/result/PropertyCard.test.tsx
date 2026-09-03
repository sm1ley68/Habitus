import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import PropertyCard from "./PropertyCard";
import { PROPERTIES } from "@/test/fixtures";
import { useSession } from "@/lib/store/session";

describe("PropertyCard", () => {
  it("shows cover, name, price and match score, and hover sets hovered id", () => {
    const onOpen = vi.fn();
    render(<PropertyCard property={PROPERTIES[0]} index={0} onOpen={onOpen} />);
    expect(screen.getByRole("img", { name: /Neva Residence/i })).toBeInTheDocument();
    expect(screen.getByText(/18.5 млн/i)).toBeInTheDocument();
    expect(screen.getByLabelText("96% совпадение")).toBeInTheDocument();
    const open = screen.getByRole("button", { name: /^Открыть / });
    // Наведение слушает карточка целиком, а не кнопка открытия: подсветка на
    // карте не должна гаснуть, когда курсор идёт к «сохранить».
    fireEvent.mouseEnter(open.parentElement!);
    expect(useSession.getState().hoveredId).toBe("jk-neva-residence");
    fireEvent.mouseLeave(open.parentElement!);
    expect(useSession.getState().hoveredId).toBe(null);
    fireEvent.click(open);
    expect(onOpen).toHaveBeenCalledWith(0);
  });

  it("показывает реальный адрес вместо синтезированного имени", () => {
    const property = { ...PROPERTIES[0], address: "Москва, 2-й Донской проезд" };
    render(<PropertyCard property={property} index={0} onOpen={() => {}} />);
    expect(screen.getByText("Москва, 2-й Донской проезд")).toBeInTheDocument();
  });

  it("откатывается к имени, когда адреса нет", () => {
    const property = { ...PROPERTIES[0], address: "" };
    render(<PropertyCard property={property} index={0} onOpen={() => {}} />);
    expect(screen.getByText("ЖК Neva Residence")).toBeInTheDocument();
  });

  it("рисует обложку, пришедшую с бэка (ссылка на CDN источника)", () => {
    const url = "https://cdn.cian.site/1-full.jpg";
    const property = { ...PROPERTIES[0], cover_image: url };
    render(<PropertyCard property={property} index={0} onOpen={() => {}} />);
    const img = screen.getByAltText(property.name) as HTMLImageElement;
    expect(img.src).toBe(url);
  });
});
