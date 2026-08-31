import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import MetroRouteStrip, { FALLBACK_COLOUR } from "./MetroRouteStrip";
import { metroRideFixture } from "@/test/fixtures";

// metroRideFixture (frontend/test/fixtures.ts): 7 мин пешком до станции →
// метро Сокольники→Охотный Ряд (6 ост., 13 мин) → пересадка улицей 8 мин →
// МЦК Белорусская→Лужники (4 ост., 9 мин, estimated) → 5 мин пешком от
// станции. wait_min=3, total_minutes=45 (и это ровно сумма частей в ЭТОЙ
// фикстуре — совпадение фикстуры, не то, на чём должен строиться компонент,
// поэтому отдельный тест ниже намеренно ломает эту сумму).

describe("MetroRouteStrip", () => {
  it("показывает пешие плечи с обоих концов", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    expect(screen.getByText(/7 мин пешком/)).toBeInTheDocument();
    expect(screen.getByText(/5 мин пешком/)).toBeInTheDocument();
  });

  it("показывает станции и число перегонов каждого отрезка", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const first = screen.getByTestId("segment-0");
    expect(first).toHaveTextContent("Сокольники");
    expect(first).toHaveTextContent("Охотный Ряд");
    expect(first).toHaveTextContent("6 станций");
  });

  it("подписывает систему словом, а не только цветом", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    // цвет никогда не единственный носитель смысла
    expect(screen.getByTestId("segment-0")).toHaveTextContent("метро");
    expect(screen.getByTestId("segment-1")).toHaveTextContent("МЦК");
  });

  it("рисует уличную пересадку отдельным пешим шагом с её минутами", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const t = screen.getByTestId("transfer-0");
    expect(t).toHaveTextContent("8 мин");
    expect(t).toHaveTextContent(/улицей/i);
  });

  it("не падает на поездке без пересадок", () => {
    const ride = {
      ...metroRideFixture,
      transfers: [],
      segments: [metroRideFixture.segments[0]],
    };
    render(<MetroRouteStrip ride={ride} />);
    expect(screen.queryByTestId("transfer-0")).toBeNull();
  });

  it("даёт маршруту доступную подпись целиком", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const list = screen.getByRole("list", { name: /маршрут/i });
    expect(list).toBeInTheDocument();
  });

  it("ставит пересадку между отрезками, которые она связывает (R7: сразу интерливинг, без промежуточной версии)", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const items = screen.getAllByRole("listitem");
    const ids = items.map((el) => el.getAttribute("data-testid"));
    expect(ids.indexOf("transfer-0")).toBeGreaterThan(ids.indexOf("segment-0"));
    expect(ids.indexOf("transfer-0")).toBeLessThan(ids.indexOf("segment-1"));
  });
});

// Каждая из четырёх честностей ниже была куплена дорогой ценой выше по
// пайплайну (Задачи 9–16) — здесь она либо соблюдена на экране, либо весь
// смысл этой цены потерян на последнем шаге.
describe("честность данных на ленте", () => {
  it("[1] помечает wait_min как остаток округления, а не измеренное время ожидания поезда", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const wait = screen.getByTestId("wait-note");
    expect(wait).toHaveTextContent("3 мин");
    expect(wait).toHaveTextContent(/остаток округления/i);
    expect(wait).not.toHaveTextContent(/время ожидания поезда/i);
  });

  it("[2] метит оценку по seg.estimated, а не по ride.estimated — оценённая пешая нога не красит рельсовые отрезки", () => {
    const ride = {
      ...metroRideFixture,
      estimated: true, // оценена только пешая нога
      segments: metroRideFixture.segments.map((s) => ({ ...s, estimated: false })),
    };
    render(<MetroRouteStrip ride={ride} />);
    expect(screen.getByTestId("segment-0")).not.toHaveTextContent(/оценка/i);
    expect(screen.getByTestId("segment-1")).not.toHaveTextContent(/оценка/i);
    // ride.estimated по-прежнему отражён — но на уровне поездки, не сегмента
    expect(screen.getByTestId("total")).toHaveTextContent(/оцен/i);
  });

  it("[2b] помечает конкретный сегмент оценкой по его собственному estimated: true", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    expect(screen.getByTestId("segment-1")).toHaveTextContent(/оценка/i);
    expect(screen.getByTestId("segment-0")).not.toHaveTextContent(/оценка/i);
  });

  it("[3] красит точку сегмента запасным цветом, если colour = null, и берёт CSS-имя цвета как есть", () => {
    const ride = {
      ...metroRideFixture,
      segments: [
        { ...metroRideFixture.segments[0], colour: null },
        metroRideFixture.segments[1], // colour: "red" — CSS-имя, не hex
      ],
    };
    render(<MetroRouteStrip ride={ride} />);
    expect(screen.getByTestId("segment-0-dot")).toHaveStyle({ background: FALLBACK_COLOUR });
    expect(screen.getByTestId("segment-1-dot")).toHaveStyle({ background: "red" });
  });

  it("[4] подписывает walk_from_home_min как оценку по прямой — не как измеренный walk_min_metro с карточки объявления", () => {
    render(<MetroRouteStrip ride={metroRideFixture} />);
    const walk = screen.getByTestId("walk-from-home");
    expect(walk).toHaveTextContent("7 мин пешком");
    // не единственная метка «до метро» в дизайне — заголовок явно называет её
    // прямой оценкой, чтобы её не спутали с измеренным walk_min_metro объекта.
    expect(walk.getAttribute("title")).toMatch(/по прямой/i);
  });

  it("показывает итог из контракта, а не пересчитанную сумму частей", () => {
    // Подменяем total_minutes значением, которое заведомо не равно сумме
    // частей текущей фикстуры (7+13+9+8+5+3=45) — если бы компонент
    // складывал части заново, тест бы это поймал.
    const ride = { ...metroRideFixture, total_minutes: 999 };
    render(<MetroRouteStrip ride={ride} />);
    expect(screen.getByTestId("total")).toHaveTextContent("999");
  });
});
