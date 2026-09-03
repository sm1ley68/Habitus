import { render, screen } from "@testing-library/react";
import LocationStep from "./LocationStep";

const MSK: [number, number] = [37.62, 55.75];
const SPB: [number, number] = [30.31, 59.94];

function renderAt(coordinates: [number, number] | null) {
  render(
    <LocationStep
      coordinates={coordinates}
      address=""
      onCoordinates={() => {}}
      onAddress={() => {}}
    />,
  );
}

test("точка в Петербурге — продавца предупреждают, что город вне поиска", () => {
  renderAt(SPB);
  expect(screen.getByText(/пока не участвует в поиске/i)).toBeInTheDocument();
});

test("точка в Москве не даёт предупреждения", () => {
  renderAt(MSK);
  expect(screen.queryByText(/пока не участвует в поиске/i)).not.toBeInTheDocument();
});

test("без точки предупреждения нет — городу ещё неоткуда взяться", () => {
  renderAt(null);
  expect(screen.queryByText(/пока не участвует в поиске/i)).not.toBeInTheDocument();
});
