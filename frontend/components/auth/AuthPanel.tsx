"use client";
import { Dialog } from "@/components/ui";
import { useAuth } from "@/lib/store/auth";
import AuthForm from "./AuthForm";

/**
 * Точка регистрации поверх работающего приложения. Открывается из LeftRail и
 * отовсюду, где гостю по делу нужен аккаунт (кабинет продавца).
 *
 * Гость здесь ничего не теряет: POST /auth/register с живой гостевой кукой —
 * это апгрейд той же строки users, а не новый пользователь.
 */
export default function AuthPanel() {
  const open = useAuth((s) => s.authOpen);
  const close = useAuth((s) => s.closeAuth);
  const setUser = useAuth((s) => s.setUser);
  const isGuest = useAuth((s) => s.user?.is_guest ?? true);

  return (
    <Dialog open={open} onClose={close} title={isGuest ? "Сохранить найденное" : "Аккаунт"}>
      <AuthForm
        initialMode={isGuest ? "register" : "login"}
        hint={
          isGuest
            ? "Аккаунт сохранит подборки, избранное и оценки — всё, что вы уже нашли, останется при вас."
            : "Войдите в другой аккаунт."
        }
        onDone={setUser}
      />
    </Dialog>
  );
}
