"use client";
import { useSession } from "@/lib/store/session";

// «Показать ещё»: остаток выдачи уже сохранён на бэке, кнопка листает его
// через GET /chats/{id}/results — повторный поиск не запускается.
export default function LoadMoreButton() {
  const hasMore = useSession((s) => s.hasMore);
  const loading = useSession((s) => s.loadingMore);
  const shown = useSession((s) => s.properties.length);
  const total = useSession((s) => s.totalResults);
  const loadMore = useSession((s) => s.loadMore);

  if (!hasMore) return null;

  return (
    <button
      type="button"
      onClick={() => void loadMore()}
      disabled={loading}
      aria-busy={loading}
      className="mt-1 w-full rounded-2xl border border-zinc-200 px-5 py-3 text-sm
                 text-zinc-700 transition hover:bg-zinc-50 active:scale-[0.99]
                 disabled:cursor-progress disabled:opacity-60
                 dark:border-zinc-700 dark:text-zinc-200 dark:hover:bg-zinc-800"
    >
      {loading ? "Загружаю…" : `Показать ещё — ${shown} из ${total}`}
    </button>
  );
}
