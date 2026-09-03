import { describe, it, expect, vi, afterEach } from "vitest";
import { me, login, guest } from "./auth";

afterEach(() => vi.unstubAllGlobals());

describe("auth", () => {
  it("me() возвращает null на 401, а не бросает", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 401 })));
    await expect(me()).resolves.toBeNull();
  });

  it("login() шлёт куку сессии и возвращает пользователя", async () => {
    const f = vi.fn(async (_url: string, _init?: RequestInit) =>
      Response.json({ id: "u1", email: "a@b.c", name: "Аня", is_guest: false }));
    vi.stubGlobal("fetch", f);
    await expect(login("a@b.c", "pw")).resolves.toEqual({
      id: "u1", email: "a@b.c", name: "Аня", is_guest: false,
    });
    expect(f.mock.calls[0][1]).toMatchObject({ credentials: "include" });
  });

  it("login() поднимает сообщение из конверта ошибки бэка", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      Response.json({ error: { code: "unauthorized", message: "Неверный email или пароль" } },
        { status: 401 })));
    await expect(login("a@b.c", "bad")).rejects.toThrow("Неверный email или пароль");
  });

  it("guest() заводит сессию без регистрации и несёт куку", async () => {
    const f = vi.fn(async (_url: string, _init?: RequestInit) =>
      Response.json({ id: "g1", email: "", name: "Гость", is_guest: true },
        { status: 201 }));
    vi.stubGlobal("fetch", f);
    await expect(guest()).resolves.toMatchObject({ id: "g1", is_guest: true });
    expect(f.mock.calls[0][0]).toContain("/auth/guest");
    expect(f.mock.calls[0][1]).toMatchObject({ method: "POST", credentials: "include" });
  });
});
