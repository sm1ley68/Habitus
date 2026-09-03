"use client";
import Link from "next/link";
import { useSession } from "@/lib/store/session";
import { useAuth } from "@/lib/store/auth";

const Icon = {
  plus: "M12 5v14M5 12h14",
  grid: "M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z",
  pin: "M12 21s7-5.5 7-11a7 7 0 10-14 0c0 5.5 7 11 7 11z",
  clock: "M12 7v5l3 2M21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  heart: "M20.8 4.6a5.5 5.5 0 00-7.8 0L12 5.6l-1-1a5.5 5.5 0 00-7.8 7.8l1 1L12 21.2l7.8-7.8 1-1a5.5 5.5 0 000-7.8z",
};

function RailBtn({
  d, label, active, onClick,
}: { d: string; label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      aria-label={label}
      aria-pressed={active}
      title={label}
      className={`grid place-items-center h-9 w-9 rounded-xl cursor-pointer transition-colors duration-150 active:scale-[0.94] ${
        active
          ? "bg-zinc-100 text-zinc-900"
          : "text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900"
      }`}
    >
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
        strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d={d} />
      </svg>
    </button>
  );
}

export default function LeftRail() {
  const { screen, setScreen, reset, toggleHistory, historyOpen } = useSession();
  // Гость приложением пользуется наравне со всеми — аккаунт нужен там, где он
  // что-то даёт: кабинет продавца и сохранение подборок. Поэтому у гостя здесь
  // приглашение зарегистрироваться, а у зарегистрированного — вход в кабинет.
  const isGuest = useAuth((s) => s.user?.is_guest ?? false);
  const openAuth = useAuth((s) => s.openAuth);
  return (
    <nav className="fixed bottom-0 inset-x-0 h-16 flex-row justify-around border-t border-zinc-100 md:static md:h-auto md:w-[56px] md:flex-col md:justify-start md:border-t-0 flex items-center gap-1 py-2 md:py-4 z-[30] bg-white shrink-0">
      {/* brand spark (desktop) */}
      <div className="hidden md:grid place-items-center h-9 w-9 mb-1.5">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="var(--accent)" aria-hidden="true">
          <path d="M12 2c.45 4.9 3.6 8.05 8.5 8.5-4.9.45-8.05 3.6-8.5 8.5-.45-4.9-3.6-8.05-8.5-8.5C8.4 10.05 11.55 6.9 12 2z" />
        </svg>
      </div>

      <RailBtn d={Icon.plus} label="Новый поиск" active={screen === "chat"} onClick={() => reset()} />
      <RailBtn d={Icon.grid} label="Результаты" active={screen === "result" || screen === "passport"} onClick={() => setScreen("result")} />
      <RailBtn d={Icon.pin} label="Карта" active={screen === "map"} onClick={() => setScreen("map")} />
      <RailBtn d={Icon.heart} label="Сохранённое" active={screen === "saved"} onClick={() => setScreen("saved")} />

      <div className="hidden md:block flex-1" />

      <RailBtn d={Icon.clock} label="История" active={historyOpen} onClick={toggleHistory} />

      {/* Вход в кабинет продавца. Раньше здесь был неинтерактивный аватар —
          единственное место, откуда пользователь ждёт перехода «к себе».
          Гостю кабинет закрыт на бэке (middleware.RequireRegistered), поэтому
          ему тут форма регистрации, а не ссылка в 403. */}
      {isGuest ? (
        <button
          onClick={openAuth}
          aria-label="Войти или зарегистрироваться"
          title="Войти"
          className="hidden md:grid place-items-center h-8 w-8 mt-1 rounded-full border border-zinc-300 text-zinc-500 text-[11px] font-medium select-none cursor-pointer transition-colors duration-150 hover:border-zinc-400 hover:text-zinc-900"
        >
          Вход
        </button>
      ) : (
        <Link
          href="/lk"
          aria-label="Личный кабинет"
          title="Личный кабинет"
          className="hidden md:grid place-items-center h-8 w-8 mt-1 rounded-full bg-gradient-to-br from-zinc-600 to-zinc-900 text-white text-[11px] font-medium select-none transition-opacity duration-150 hover:opacity-85"
        >
          Я
        </Link>
      )}
    </nav>
  );
}
