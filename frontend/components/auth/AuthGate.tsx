"use client";
import { useEffect, useState, type FormEvent } from "react";
import { me, login, register, type User } from "@/lib/api/auth";

// Все /api/v1 роуты, кроме auth/*, закрыты сессионной кукой (Go: middleware.Auth).
// Без входа приложение получит 401 на каждый запрос, поэтому шелл рендерится
// только поверх живой сессии.
type Mode = "login" | "register";

const field =
  "rounded-lg border border-zinc-200 px-3 py-2 text-[#1c1d20] outline-none transition-colors focus:border-accent";

export default function AuthGate({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [checked, setChecked] = useState(false);
  const [mode, setMode] = useState<Mode>("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    me()
      .then((u) => { if (alive) setUser(u); })
      .catch(() => { if (alive) setUser(null); })
      .finally(() => { if (alive) setChecked(true); });
    return () => { alive = false; };
  }, []);

  if (user) return <>{children}</>;

  if (!checked) {
    return (
      <div className="flex-1 grid place-items-center text-sm text-zinc-400">
        Проверяем сессию…
      </div>
    );
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const u = mode === "login"
        ? await login(email, password)
        : await register(email, password, name || email);
      setUser(u);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Не удалось войти");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex-1 grid place-items-center px-4">
      <form onSubmit={submit} className="flex w-full max-w-sm flex-col gap-4">
        <div>
          <h1 className="text-xl tracking-tight text-[#1c1d20]">
            {mode === "login" ? "Вход" : "Регистрация"}
          </h1>
          <p className="mt-1 text-sm text-zinc-500">
            Habitus подбирает жильё по тому, как вы живёте.
          </p>
        </div>

        {mode === "register" && (
          <label className="flex flex-col gap-1 text-sm text-zinc-500">
            Имя
            <input
              className={field}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoComplete="name"
            />
          </label>
        )}

        <label className="flex flex-col gap-1 text-sm text-zinc-500">
          Email
          <input
            className={field}
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="email"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm text-zinc-500">
          Пароль
          <input
            className={field}
            type="password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={mode === "login" ? "current-password" : "new-password"}
          />
        </label>

        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}

        <button
          type="submit"
          disabled={busy}
          className="rounded-lg bg-[#1c1d20] px-3 py-2.5 text-white transition-opacity hover:opacity-90 disabled:opacity-50"
        >
          {busy ? "…" : mode === "login" ? "Войти" : "Зарегистрироваться"}
        </button>

        <button
          type="button"
          className="text-sm text-zinc-400 transition-colors hover:text-zinc-600"
          onClick={() => { setMode(mode === "login" ? "register" : "login"); setError(null); }}
        >
          {mode === "login" ? "Нет аккаунта? Регистрация" : "Уже есть аккаунт? Войти"}
        </button>
      </form>
    </div>
  );
}
