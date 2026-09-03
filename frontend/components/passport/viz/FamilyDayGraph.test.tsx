import { render, screen } from "@testing-library/react";
import { it, expect } from "vitest";
import FamilyDayGraph from "./FamilyDayGraph";
import { LIFESTYLE_BLOCKS } from "@/test/fixtures";

const block = LIFESTYLE_BLOCKS.find((b) => b.key === "family_routing")!;

it("renders a day lane per household member", () => {
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
  const data = block.data as { members: { label: string }[] };
  for (const m of data.members) {
    expect(screen.getByText(m.label)).toBeInTheDocument();
  }
});

// Задача 17: нога с mode: "metro" и заполненным leg.metro разворачивает
// MetroRouteStrip под сжатой строкой Гантта.
it("разворачивает ленту метро под ногой с mode: metro и заполненным leg.metro", () => {
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
  // metroRideFixture: рельсовый отрезок Сокольники → Охотный Ряд, дальше
  // пересадка на Охотный Ряд → Белорусская — «Охотный Ряд» встречается дважды
  // (конец сегмента и начало пересадки), поэтому проверяем через getAllByText.
  expect(screen.getByText(/Сокольники/)).toBeInTheDocument();
  expect(screen.getAllByText(/Охотный Ряд/).length).toBeGreaterThan(0);
});

// R84b: metro: null и отсутствующий ключ metro у обычной пешей/metro-ноги
// должны рендерить отсутствие, а не пустую или синтетическую ленту —
// «Лужники» встречается только внутри MetroRouteStrip (to_station второго
// сегмента metroRideFixture), поэтому его отсутствие в DOM — прямой сигнал,
// что лента не отрисовалась.
function withMomMetroLeg(patch: (leg: Record<string, unknown>) => Record<string, unknown>) {
  const data = block.data as {
    home: unknown;
    members: { id: string; label: string; legs: Record<string, unknown>[] }[];
  };
  return {
    ...data,
    members: data.members.map((m) =>
      m.id !== "mom"
        ? m
        : {
            ...m,
            legs: m.legs.map((leg) => (leg.mode === "metro" ? patch(leg) : leg)),
          },
    ),
  };
}

it("[R84b] не рисует ленту, когда leg.metro === null", () => {
  const data = withMomMetroLeg((leg) => ({ ...leg, metro: null }));
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={data} />);
  expect(screen.queryByText(/Лужники/)).toBeNull();
});

it("[R84b] не рисует ленту, когда ключ metro вовсе отсутствует", () => {
  const data = withMomMetroLeg((leg) =>
    Object.fromEntries(Object.entries(leg).filter(([key]) => key !== "metro")),
  );
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={data} />);
  expect(screen.queryByText(/Лужники/)).toBeNull();
});

// Задача 3 (УТП «жизнь домохозяйства»): NLU запрещено выдумывать часы поездок,
// и пользователь их обычно не называет. Раньше нога без depart/arrive
// выбрасывалась в ML вместе со всем блоком; теперь она доезжает без времени, и
// виз обязан показать маршруты, а не рисовать их в начале суток.
function untimed() {
  const data = block.data as {
    home: unknown;
    members: { id: string; label: string; legs: Record<string, unknown>[] }[];
  };
  return {
    ...data,
    members: data.members.map((m) => ({
      ...m,
      legs: m.legs.map((leg) => ({
        ...leg, depart: null, arrive: null, safety: null, estimated: true,
      })),
    })),
  };
}

it("без часов показывает маршруты и длительность вместо суточной ленты", () => {
  render(<FamilyDayGraph metrics={{}} data={untimed()} />);
  expect(screen.getByText(/Время поездок не названо/)).toBeInTheDocument();
  // Скраббер — часть временной ленты: двигать нечего, показывать его нельзя.
  expect(screen.queryByLabelText("Время суток")).not.toBeInTheDocument();
  // Сами поездки на месте, вместе с измеренной длительностью.
  const data = block.data as { members: { label: string; legs: { to_label: string }[] }[] };
  for (const m of data.members) {
    expect(screen.getByText(m.label)).toBeInTheDocument();
    for (const leg of m.legs) {
      expect(screen.getAllByText(leg.to_label).length).toBeGreaterThan(0);
    }
  }
  expect(screen.getAllByText(/\d+ мин/).length).toBeGreaterThan(0);
});

it("оценка по прямой помечена как оценка, а не выдана за маршрут", () => {
  render(<FamilyDayGraph metrics={{}} data={untimed()} />);
  expect(screen.getAllByText("оценка").length).toBeGreaterThan(0);
  expect(screen.getByText(/посчитано по прямой, а не по сети/)).toBeInTheDocument();
});

it("с полным временем суточная лента остаётся на месте", () => {
  render(<FamilyDayGraph metrics={block.metrics ?? {}} data={block.data} />);
  expect(screen.getByLabelText("Время суток")).toBeInTheDocument();
  expect(screen.queryByText(/Время поездок не названо/)).not.toBeInTheDocument();
});
