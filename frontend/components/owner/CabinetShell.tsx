import Link from "next/link";
import type { ReactNode } from "react";

/**
 * Каркас кабинета. LeftRail сюда не тянем: кабинет — отдельный контекст со
 * своей задачей, и рельс поиска в нём только сбивал бы с толку.
 */
export default function CabinetShell({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-dvh bg-zinc-50/60">
      <header className="border-b border-zinc-200 bg-white">
        <div className="mx-auto flex max-w-4xl flex-wrap items-center justify-between gap-3 px-5 py-4">
          <Link href="/lk" className="text-[15px] tracking-tight text-[#1c1d20]">
            Мои объявления
          </Link>
          <nav className="flex items-center gap-4 text-sm">
            <Link href="/" className="text-zinc-500 transition-colors hover:text-[#1c1d20]">
              К поиску
            </Link>
            <Link href="/lk/profile" className="text-zinc-500 transition-colors hover:text-[#1c1d20]">
              Профиль
            </Link>
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-4xl px-5 py-8">{children}</main>
    </div>
  );
}
