import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AuthGate from "./AuthGate";

const mocks = vi.hoisted(() => ({ me: vi.fn(), login: vi.fn(), register: vi.fn() }));
vi.mock("@/lib/api/auth", () => mocks);

beforeEach(() => vi.clearAllMocks());

describe("AuthGate", () => {
  it("показывает форму входа, когда сессии нет", async () => {
    mocks.me.mockResolvedValue(null);
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByLabelText("Email")).toBeInTheDocument();
    expect(screen.queryByText("секрет")).not.toBeInTheDocument();
  });

  it("пускает внутрь при живой сессии", async () => {
    mocks.me.mockResolvedValue({ id: "u1", email: "a@b.c", name: "Аня" });
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByText("секрет")).toBeInTheDocument();
  });

  it("после успешного входа рендерит приложение", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.login.mockResolvedValue({ id: "u1", email: "a@b.c", name: "Аня" });
    render(<AuthGate><div>секрет</div></AuthGate>);
    await userEvent.type(await screen.findByLabelText("Email"), "a@b.c");
    await userEvent.type(screen.getByLabelText("Пароль"), "pw");
    await userEvent.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByText("секрет")).toBeInTheDocument();
  });

  it("показывает ошибку бэка и не пускает внутрь", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.login.mockRejectedValue(new Error("Неверный email или пароль"));
    render(<AuthGate><div>секрет</div></AuthGate>);
    await userEvent.type(await screen.findByLabelText("Email"), "a@b.c");
    await userEvent.type(screen.getByLabelText("Пароль"), "bad");
    await userEvent.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Неверный email или пароль");
    expect(screen.queryByText("секрет")).not.toBeInTheDocument();
  });
});
