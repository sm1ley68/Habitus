import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AuthGate from "./AuthGate";
import { useAuth } from "@/lib/store/auth";

const mocks = vi.hoisted(() => ({
  me: vi.fn(), login: vi.fn(), register: vi.fn(), guest: vi.fn(), logout: vi.fn(),
}));
vi.mock("@/lib/api/auth", () => mocks);

beforeEach(() => {
  vi.clearAllMocks();
  useAuth.setState({ user: null, status: "checking", authOpen: false });
});

const GUEST = { id: "g1", email: "", name: "Гость", is_guest: true };

describe("AuthGate", () => {
  it("без сессии заводит гостя и пускает внутрь — стены регистрации нет", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.guest.mockResolvedValue(GUEST);
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByText("секрет")).toBeInTheDocument();
    expect(mocks.guest).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText("Пароль")).not.toBeInTheDocument();
  });

  it("при живой сессии гостя не заводит", async () => {
    mocks.me.mockResolvedValue({ id: "u1", email: "a@b.c", name: "Аня", is_guest: false });
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByText("секрет")).toBeInTheDocument();
    expect(mocks.guest).not.toHaveBeenCalled();
  });

  it("показывает форму, только когда шлюз не отдал даже гостя", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.guest.mockRejectedValue(new Error("шлюз недоступен"));
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByLabelText("Email")).toBeInTheDocument();
    expect(screen.queryByText("секрет")).not.toBeInTheDocument();
  });

  it("после входа с аварийного экрана рендерит приложение", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.guest.mockRejectedValue(new Error("шлюз недоступен"));
    mocks.login.mockResolvedValue({ id: "u1", email: "a@b.c", name: "Аня", is_guest: false });
    render(<AuthGate><div>секрет</div></AuthGate>);
    await userEvent.type(await screen.findByLabelText("Email"), "a@b.c");
    await userEvent.type(screen.getByLabelText("Пароль"), "pw");
    await userEvent.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByText("секрет")).toBeInTheDocument();
  });

  it("показывает ошибку бэка и не пускает внутрь", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.guest.mockRejectedValue(new Error("шлюз недоступен"));
    mocks.login.mockRejectedValue(new Error("Неверный email или пароль"));
    render(<AuthGate><div>секрет</div></AuthGate>);
    await userEvent.type(await screen.findByLabelText("Email"), "a@b.c");
    await userEvent.type(screen.getByLabelText("Пароль"), "bad");
    await userEvent.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Неверный email или пароль");
    expect(screen.queryByText("секрет")).not.toBeInTheDocument();
  });
});
