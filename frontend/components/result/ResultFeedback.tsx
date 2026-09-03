"use client";
import { useState } from "react";
import { useEngagement } from "@/lib/store/engagement";
import { useSession } from "@/lib/store/session";

/**
 * «Подходит / не подходит» по объекту из выдачи — единственный продакшн-сигнал
 * о качестве подбора. Доступен и гостю.
 *
 * Оценка принадлежит паре (чат, объект): оценивать можно только то, что было в
 * выдаче ЭТОГО чата, иначе шлюз отвечает 404. Поэтому без chatId блок не
 * рисуется вовсе — кнопка, которая гарантированно откажет, хуже её отсутствия.
 *
 * У «не подходит» спрашивается причина: вердикт без причины говорит, что
 * подбор промахнулся, но не говорит чем, а чинить нужно именно это.
 */
export default function ResultFeedback({ objectId }: { objectId: string }) {
  const chatId = useSession((s) => s.chatId);
  // Оценка принадлежит паре (чат, объект): вердикт, поставленный в прошлом
  // диалоге, к текущей выдаче отношения не имеет и подсвечиваться не должен.
  const verdict = useEngagement((s) =>
    (s.verdictsChatId === chatId ? s.verdicts[objectId] : undefined));
  const rate = useEngagement((s) => s.rate);
  const [reasonOpen, setReasonOpen] = useState(false);
  const [reason, setReason] = useState("");

  if (!chatId) return null;

  const stop = (e: React.SyntheticEvent) => e.stopPropagation();

  return (
    <div onClick={stop} className="mt-3 border-t border-zinc-100 pt-3">
      <div className="flex items-center gap-2">
        <span className="text-[11px] text-zinc-400">Подходит?</span>
        <button
          type="button"
          aria-pressed={verdict === "up"}
          aria-label="Подходит"
          onClick={(e) => { stop(e); setReasonOpen(false); void rate(chatId, objectId, "up"); }}
          className={`grid h-7 w-7 place-items-center rounded-lg border transition-colors cursor-pointer ${
            verdict === "up"
              ? "border-[#2f8f5f] bg-[#eaf6ef] text-[#2f8f5f]"
              : "border-zinc-200 text-zinc-400 hover:border-zinc-300 hover:text-zinc-600"
          }`}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M7 22V11l5-9a2.5 2.5 0 012.4 3.2L13.5 9H19a2 2 0 012 2.4l-1.6 8A2 2 0 0117.4 21H7z" />
          </svg>
        </button>
        <button
          type="button"
          aria-pressed={verdict === "down"}
          aria-label="Не подходит"
          onClick={(e) => { stop(e); setReasonOpen(true); }}
          className={`grid h-7 w-7 place-items-center rounded-lg border transition-colors cursor-pointer ${
            verdict === "down"
              ? "border-[#b25e4a] bg-[#f6ece9] text-[#b25e4a]"
              : "border-zinc-200 text-zinc-400 hover:border-zinc-300 hover:text-zinc-600"
          }`}
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
            strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M17 2v11l-5 9a2.5 2.5 0 01-2.4-3.2L10.5 15H5a2 2 0 01-2-2.4l1.6-8A2 2 0 016.6 3H17z" />
          </svg>
        </button>
        {verdict && !reasonOpen && (
          <span className="text-[11px] text-zinc-400">Учли</span>
        )}
      </div>

      {reasonOpen && (
        <div className="mt-2 flex items-center gap-2">
          <input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            onClick={stop}
            maxLength={500}
            placeholder="Что не так? Например: далеко от метро"
            aria-label="Почему не подходит"
            className="min-h-8 flex-1 rounded-lg border border-zinc-200 px-2.5 py-1 text-xs text-[#1c1d20] outline-none transition-colors placeholder:text-zinc-400 focus:border-accent"
          />
          <button
            type="button"
            onClick={(e) => {
              stop(e);
              void rate(chatId, objectId, "down", reason.trim() || undefined);
              setReasonOpen(false);
            }}
            className="min-h-8 rounded-lg bg-[#1c1d20] px-2.5 text-xs text-white transition-opacity hover:opacity-90 cursor-pointer"
          >
            Отправить
          </button>
        </div>
      )}
    </div>
  );
}
