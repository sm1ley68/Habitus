"use client";
import { useEffect } from "react";
import { useAuth } from "@/lib/store/auth";
import AuthForm from "./AuthForm";

/**
 * Сессия до приложения — но НЕ стена регистрации.
 *
 * Все /api/v1 роуты, кроме auth/*, закрыты сессионной кукой (Go:
 * middleware.Auth), поэтому какая-то сессия нужна всегда. Раньше отсюда
 * требовали аккаунт, и стена стояла ровно там, где у продукта единственный
 * шанс показать ценность. Теперь, если живой сессии нет, заводится гостевая
 * (POST /auth/guest), и первый поиск проходит без регистрации. Регистрация
 * из-под гостя — апгрейд того же пользователя: id не меняется, чаты,
 * избранное и оценки остаются при нём.
 *
 * Форма входа остаётся здесь только как аварийный экран: шлюз не отдал даже
 * гостевую сессию, и больше пользователю тут делать нечего. Обычная точка
 * регистрации живёт в AuthPanel, поверх работающего приложения.
 */
export default function AuthGate({ children }: { children: React.ReactNode }) {
  const status = useAuth((s) => s.status);
  const ensureSession = useAuth((s) => s.ensureSession);
  const setUser = useAuth((s) => s.setUser);

  useEffect(() => { void ensureSession(); }, [ensureSession]);

  if (status === "ready") return <>{children}</>;

  if (status === "checking") {
    return (
      <div className="flex-1 grid place-items-center text-sm text-zinc-400">
        Проверяем сессию…
      </div>
    );
  }

  return (
    <div className="flex-1 grid place-items-center px-4">
      <AuthForm
        hint="Не удалось начать сессию. Войдите, чтобы продолжить."
        onDone={setUser}
      />
    </div>
  );
}
