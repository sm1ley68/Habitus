import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Composer from "./Composer";

test("submits trimmed text and clears the field", async () => {
  const onSubmit = vi.fn();
  render(<Composer onSubmit={onSubmit} />);
  const input = screen.getByLabelText("Запрос агенту");
  await userEvent.type(input, "  тихий двор  ");
  await userEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
  expect(onSubmit).toHaveBeenCalledWith("тихий двор");
  expect(input).toHaveValue("");
});

test("does not submit empty input", async () => {
  const onSubmit = vi.fn();
  render(<Composer onSubmit={onSubmit} />);
  await userEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
  expect(onSubmit).not.toHaveBeenCalled();
});
