import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import { Badge, Button, Field, Input } from "./index";

test("Button в состоянии загрузки недоступен и объявляет это ассистивным технологиям", () => {
  render(<Button loading>Опубликовать</Button>);
  const button = screen.getByRole("button", { name: /опубликовать/i });
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute("aria-busy", "true");
});

test("Button не срабатывает во время загрузки", async () => {
  const onClick = vi.fn();
  render(<Button loading onClick={onClick}>Опубликовать</Button>);
  await userEvent.click(screen.getByRole("button"));
  expect(onClick).not.toHaveBeenCalled();
});

test("Field связывает лейбл, подсказку и ошибку с полем", () => {
  render(
    <Field label="Цена" hint="В рублях" error="Укажите цену">
      <Input />
    </Field>,
  );
  const input = screen.getByLabelText("Цена");
  expect(input).toHaveAttribute("aria-invalid", "true");
  const describedBy = input.getAttribute("aria-describedby") ?? "";
  expect(describedBy.split(" ").length).toBe(2);
  expect(screen.getByText("Укажите цену")).toBeInTheDocument();
  expect(screen.getByText("В рублях")).toBeInTheDocument();
});

test("Field без ошибки не помечает поле невалидным", () => {
  render(
    <Field label="Площадь">
      <Input />
    </Field>,
  );
  expect(screen.getByLabelText("Площадь")).not.toHaveAttribute("aria-invalid", "true");
});

test("Badge статуса читается текстом, а не только цветом", () => {
  render(<Badge tone="warn">Черновик</Badge>);
  expect(screen.getByText("Черновик")).toBeInTheDocument();
});
