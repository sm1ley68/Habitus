"use client";
import { useSession } from "@/lib/store/session";

// Какое условие обнулило выборку: последний шаг, на котором ещё что-то
// оставалось, и первый, на котором стало ноль. Диагностика приходит из ML
// уже в порядке наложения клауз, поэтому достаточно одного прохода.
function culprit(steps: { constraint: string; remaining: number }[]) {
  const zeroAt = steps.findIndex((s) => s.remaining === 0);
  if (zeroAt <= 0) return null;
  return { step: steps[zeroAt], before: steps[zeroAt - 1] };
}

export default function EmptyResult() {
  const diagnostics = useSession((s) => s.diagnostics);
  const found = culprit(diagnostics);

  return (
    <div className="text-center max-w-md mx-auto">
      <h2 className="text-xl font-medium">В этой зоне пока нет точных совпадений</h2>

      {found ? (
        <p className="mt-2 text-zinc-500">
          Выборку обнулило условие «{found.step.constraint}»: до него подходило{" "}
          {found.before.remaining}, после — ни одного.
        </p>
      ) : (
        <p className="mt-2 text-zinc-500">
          Попробуйте ослабить условия — например, увеличить время до школы или
          поднять потолок бюджета.
        </p>
      )}

      {diagnostics.length > 0 && (
        <ol className="mt-4 text-left text-sm text-zinc-500">
          {diagnostics.map((d) => (
            <li
              key={d.constraint}
              className="flex justify-between border-b border-zinc-100 py-1
                         last:border-0 dark:border-zinc-800"
            >
              <span>{d.constraint}</span>
              <span className={d.remaining === 0 ? "text-red-500" : ""}>
                {d.remaining}
              </span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
