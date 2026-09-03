import { create } from "zustand";
import { guest, me, type User } from "@/lib/api/auth";

// "checking" — идёт первичная проверка; "ready" — сессия есть (гостевая или
// зарегистрированная); "failed" — не удалось завести даже гостевую, значит
// шлюз недоступен и показывать приложение нечем.
export type AuthStatus = "checking" | "ready" | "failed";

interface AuthState {
  user: User | null;
  status: AuthStatus;
  /** Открыта ли форма входа/регистрации поверх приложения. */
  authOpen: boolean;
  ensureSession: () => Promise<void>;
  setUser: (u: User) => void;
  openAuth: () => void;
  closeAuth: () => void;
}

let sessionRequest: Promise<void> | null = null;

export const useAuth = create<AuthState>((set) => ({
  user: null,
  status: "checking",
  authOpen: false,

  // Стены перед первым поиском нет: если живой сессии не нашлось, заводим
  // гостевую и пускаем внутрь. Ценность продукта видна до того, как он
  // что-то просит взамен, а регистрация из-под гостя — апгрейд того же
  // пользователя: id не меняется, чаты и избранное остаются при нём.
  //
  // Запрос дедуплицируется: StrictMode в dev монтирует эффекты дважды, и без
  // этого приложение стартовало бы двумя параллельными /auth/guest.
  ensureSession: () => {
    if (sessionRequest) return sessionRequest;
    sessionRequest = (async () => {
      try {
        const current = await me();
        if (current) {
          set({ user: current, status: "ready" });
          return;
        }
        set({ user: await guest(), status: "ready" });
      } catch {
        // Шлюз недоступен — ни гостя, ни входа. Показываем форму: она
        // единственное, что пользователь может здесь сделать сам.
        set({ status: "failed" });
      } finally {
        sessionRequest = null;
      }
    })();
    return sessionRequest;
  },

  setUser: (user) => set({ user, status: "ready", authOpen: false }),
  openAuth: () => set({ authOpen: true }),
  closeAuth: () => set({ authOpen: false }),
}));
