"use client";
import { useEffect } from "react";
import { useEngagement } from "@/lib/store/engagement";
import { useSession } from "@/lib/store/session";

/**
 * Сохранение объекта в избранное. Доступно и гостю: сохранённое переживает
 * регистрацию, потому что апгрейд гостя не меняет id пользователя.
 *
 * chat_id передаётся, чтобы избранное помнило, из какого подбора объект
 * сохранён, — паспорт потом откроется с досье под тот же запрос.
 */
export default function SaveButton({
  objectId, label, className = "",
}: { objectId: string; label: string; className?: string }) {
  const saved = useEngagement((s) => Boolean(s.saved[objectId]));
  const toggle = useEngagement((s) => s.toggleFavorite);
  const hydrate = useEngagement((s) => s.hydrate);
  const chatId = useSession((s) => s.chatId);

  useEffect(() => { void hydrate(); }, [hydrate]);

  return (
    <button
      type="button"
      aria-pressed={saved}
      aria-label={saved ? `Убрать из сохранённого: ${label}` : `Сохранить: ${label}`}
      title={saved ? "Убрать из сохранённого" : "Сохранить"}
      onClick={(e) => {
        // Карточка целиком — кнопка «открыть»; сохранение не должно её звать.
        e.stopPropagation();
        void toggle(objectId, chatId);
      }}
      className={`grid h-9 w-9 place-items-center rounded-full backdrop-blur-md transition-colors cursor-pointer focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
        saved
          ? "bg-white text-[#b25e4a]"
          : "bg-black/30 text-white/90 hover:bg-black/45 hover:text-white"
      } ${className}`}
    >
      <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true"
        fill={saved ? "currentColor" : "none"} stroke="currentColor"
        strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M20.8 4.6a5.5 5.5 0 00-7.8 0L12 5.6l-1-1a5.5 5.5 0 00-7.8 7.8l1 1L12 21.2l7.8-7.8 1-1a5.5 5.5 0 000-7.8z" />
      </svg>
    </button>
  );
}
