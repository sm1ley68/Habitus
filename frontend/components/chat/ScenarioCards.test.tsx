import { render, screen, fireEvent } from "@testing-library/react";
import ScenarioCards from "./ScenarioCards";

it("показывает три сценария и отдаёт текст по клику", () => {
  const onPick = vi.fn();
  render(<ScenarioCards onPick={onPick} />);
  const cards = screen.getAllByRole("button");
  expect(cards).toHaveLength(3);
  fireEvent.click(cards[0]);
  // подставляется целая формулировка, а не ключевое слово: человек должен
  // увидеть, какого уровня подробности от него ждут
  expect(onPick.mock.calls[0][0].length).toBeGreaterThan(40);
});
