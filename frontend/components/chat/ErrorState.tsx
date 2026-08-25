"use client";
import { useSession } from "@/lib/store/session";

// Экран рисовал «Что-то пошло не так» на любой отказ — при том, что шлюз давно
// присылал конкретный текст, а store клал его в errorMessage. Диагноз, честно
// посчитанный в ML и обогащённый шлюзом, умирал на последнем шаге.
//
// Три уровня, по убыванию адресата:
//   message — что произошло, пользователю;
//   hint    — что с этим делать, ему же;
//   cause   — техническая улика (ручка ML, статус, стадия), уже разработчику.
// Каждый блок рисуется только когда поле пришло: пустой блок соврал бы, что
// причина известна и она пустая.
export default function ErrorState() {
  const reset = useSession((s) => s.reset);
  const message = useSession((s) => s.errorMessage);
  const code = useSession((s) => s.errorCode);
  const cause = useSession((s) => s.errorCause);
  const hint = useSession((s) => s.errorHint);

  return (
    <div className="text-center max-w-xl mx-auto">
      <h2 className="text-xl font-medium">Что-то пошло не так</h2>

      <p className="mt-2 text-zinc-600">
        {message || "Не удалось завершить поиск"}
      </p>

      {hint && (
        <p data-testid="error-hint" className="mt-3 text-sm text-zinc-500">
          {hint}
        </p>
      )}

      {cause && (
        <p
          data-testid="error-cause"
          className="mt-3 rounded-lg bg-zinc-50 px-3 py-2 text-left font-mono text-xs
                     leading-relaxed text-zinc-500 break-words"
        >
          {cause}
        </p>
      )}

      {code && (
        <p className="mt-2 font-mono text-[11px] uppercase tracking-wide text-zinc-400">
          {code}
        </p>
      )}

      <p className="mt-4 text-sm text-zinc-500">Данные не потеряются.</p>
      <button
        onClick={reset}
        className="mt-2 rounded-full border border-zinc-200 px-5 py-2.5 text-sm"
      >
        Попробовать снова
      </button>
    </div>
  );
}
