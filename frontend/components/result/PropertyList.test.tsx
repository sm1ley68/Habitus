import { render, screen, act } from "@testing-library/react";
import PropertyList from "./PropertyList";
import { useSession } from "@/lib/store/session";
import { PROPERTIES, runResult } from "@/test/fixtures";

test("renders one card per property with match scores", () => {
  act(() =>
    useSession.getState().finish(runResult({ properties: PROPERTIES })),
  );
  render(<PropertyList />);
  // карточка ведёт адресом, имя — фолбэк (см. PropertyCard.test)
  expect(screen.getByText(PROPERTIES[0].address)).toBeInTheDocument();
  expect(screen.getByLabelText("96% совпадение")).toBeInTheDocument();
  // Карточка больше не одна кнопка: считаем именно кнопки открытия, иначе
  // тест ломался бы от любого нового действия на карточке.
  expect(screen.getAllByRole("button", { name: /^Открыть / }).length)
    .toBe(PROPERTIES.length);
});
