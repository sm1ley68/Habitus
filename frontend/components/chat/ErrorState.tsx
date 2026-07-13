"use client";
import { useSession } from "@/lib/store/session";
export default function ErrorState() {
  const reset = useSession((s) => s.reset);
  return (
    <div className="text-center max-w-md mx-auto">
      <h2 className="text-xl font-medium">Что-то пошло не так</h2>
      <p className="mt-2 text-zinc-500">Не удалось завершить поиск. Попробуем ещё раз — данные не потеряются.</p>
      <button onClick={reset} className="mt-4 rounded-full border border-zinc-200 px-5 py-2.5 text-sm">Попробовать снова</button>
    </div>
  );
}
