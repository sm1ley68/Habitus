"use client";
import { useState, type FormEvent } from "react";
import { login, register, type User } from "@/lib/api/auth";
import { Input } from "@/components/ui";

type Mode = "login" | "register";

export interface AuthFormProps {
  /** Что показать над полями — зависит от того, откуда форму открыли. */
  hint?: string;
  /** С чего начать. Гостю, который пришёл сохранить найденное, нужна
   *  регистрация; аварийному экрану без сессии — вход. */
  initialMode?: Mode;
  onDone: (user: User) => void;
}

/**
 * Вход и регистрация одной формой. Вынесена из AuthGate, потому что теперь
 * нужна в двух местах: как аварийный экран (шлюз не отдал даже гостя) и как
 * обычная точка регистрации поверх работающего приложения.
 *
 * Регистрация из-под гостевой сессии — апгрейд той же строки users на бэке
 * (POST /auth/register с живой гостевой кукой), поэтому здесь нет ничего
 * особенного для гостя: тот же запрос, тот же id на выходе.
 */
export default function AuthForm({ hint, initialMode = "login", onDone }: AuthFormProps) {
  const [mode, setMode] = useState<Mode>(initialMode);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const u = mode === "login"
        ? await login(email, password)
        : await register(email, password, name || email);
      onDone(u);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Не удалось войти");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex w-full max-w-sm flex-col gap-4">
      <div>
        <h2 className="text-xl tracking-tight text-[#1c1d20]">
          {mode === "login" ? "Вход" : "Регистрация"}
        </h2>
        <p className="mt-1 text-sm text-zinc-500">
          {hint ?? "Habitus подбирает жильё по тому, как вы живёте."}
        </p>
      </div>

      {mode === "register" && (
        <label className="flex flex-col gap-1 text-sm text-zinc-500">
          Имя
          <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" />
        </label>
      )}

      <label className="flex flex-col gap-1 text-sm text-zinc-500">
        Email
        <Input
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          autoComplete="email"
          required
        />
      </label>

      <label className="flex flex-col gap-1 text-sm text-zinc-500">
        Пароль
        <Input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete={mode === "login" ? "current-password" : "new-password"}
          required
        />
      </label>

      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}

      <button
        type="submit"
        disabled={busy}
        className="min-h-11 rounded-lg bg-[#1c1d20] px-4 text-sm text-white transition-opacity hover:opacity-90 disabled:opacity-50"
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
  );
}
