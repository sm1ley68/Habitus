import { render, screen, act } from "@testing-library/react";
import { beforeEach, expect, test } from "vitest";
import ErrorState from "./ErrorState";
import { useSession } from "@/lib/store/session";

// Экран ошибки рисовал «Что-то пошло не так» ВСЕГДА — при том, что шлюз уже
// присылал конкретный текст, а store его сохранял в errorMessage. Вся работа
// по конкретизации отказов умирала на последнем шаге, у самого экрана.

beforeEach(() => act(() => useSession.getState().reset()));

test("показывает текст отказа от шлюза, а не общую фразу", () => {
  act(() => useSession.setState({
    stage: "error",
    errorMessage: "Нет связи с базой: connection refused",
  }));

  render(<ErrorState />);

  expect(screen.getByText("Нет связи с базой: connection refused")).toBeInTheDocument();
});

test("причина и подсказка показываются, когда они есть", () => {
  act(() => useSession.setState({
    stage: "error",
    errorMessage: "Сервис поиска не отвечает",
    errorCause: "ML /search; dial tcp 172.18.0.3:8000: connect: connection refused",
    errorHint: "Проверьте, поднят ли ML-контейнер и куда смотрит ML_SERVICE_URL",
  }));

  render(<ErrorState />);

  expect(screen.getByText(/dial tcp 172\.18\.0\.3:8000/)).toBeInTheDocument();
  expect(screen.getByText(/ML_SERVICE_URL/)).toBeInTheDocument();
});

test("без причины и подсказки лишних блоков нет", () => {
  act(() => useSession.setState({
    stage: "error",
    errorMessage: "Внутренняя ошибка сервера",
  }));

  render(<ErrorState />);

  expect(screen.getByText("Внутренняя ошибка сервера")).toBeInTheDocument();
  expect(screen.queryByTestId("error-cause")).toBeNull();
  expect(screen.queryByTestId("error-hint")).toBeNull();
});

test("отказ без текста деградирует до общей фразы", () => {
  // Поток мог оборваться до события error — сказать нечего, и выдумывать
  // причину нельзя, но экран обязан остаться осмысленным.
  act(() => useSession.setState({ stage: "error", errorMessage: null }));

  render(<ErrorState />);

  expect(screen.getByText("Не удалось завершить поиск")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Попробовать снова" })).toBeInTheDocument();
});

test("код отказа показывается — по нему ищут в логах", () => {
  act(() => useSession.setState({
    stage: "error",
    errorCode: "db_schema_missing",
    errorMessage: "Схема базы не совпадает с кодом",
  }));

  render(<ErrorState />);

  expect(screen.getByText("db_schema_missing")).toBeInTheDocument();
});
