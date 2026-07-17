"use client";
import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { useSession } from "@/lib/store/session";
import { listChats, type Chat } from "@/lib/api/chats";
import { SPRING } from "@/lib/motion";
import CitySwitch from "./CitySwitch";

// Время создания чата в короткую человеческую форму: сегодня — часы, иначе дата.
function when(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  const sameDay =
    d.getDate() === today.getDate() &&
    d.getMonth() === today.getMonth() &&
    d.getFullYear() === today.getFullYear();
  return sameDay
    ? d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString("ru-RU", { day: "numeric", month: "short" });
}

export default function HistorySidebar() {
  const open = useSession((s) => s.historyOpen);
  const chatId = useSession((s) => s.chatId);
  const [chats, setChats] = useState<Chat[] | null>(null);

  // Перечитываем при каждом открытии и после нового поиска — шлюз переименовывает
  // чат по первому запросу (событие chat_renamed), так что заголовок меняется.
  useEffect(() => {
    if (!open) return;
    let alive = true;
    listChats()
      .then((c) => { if (alive) setChats(c); })
      .catch(() => { if (alive) setChats([]); });
    return () => { alive = false; };
  }, [open, chatId]);

  return (
    <AnimatePresence>
      {open && (
        <motion.aside
          initial={{ x: -280, opacity: 0 }} animate={{ x: 0, opacity: 1 }} exit={{ x: -280, opacity: 0 }}
          transition={SPRING.gentle}
          className="w-72 shrink-0 border-r border-zinc-100 p-4 flex flex-col gap-4 bg-white z-[2]">
          <CitySwitch />
          <div className="flex flex-col gap-1">
            {chats === null && (
              <p className="px-3 py-2 text-xs text-zinc-400">Загружаем…</p>
            )}
            {chats?.length === 0 && (
              <p className="px-3 py-2 text-xs text-zinc-400">Здесь появятся ваши запросы</p>
            )}
            {chats?.map((c) => (
              <button key={c.id} className="text-left rounded-lg px-3 py-2 hover:bg-zinc-100">
                <p className="text-sm text-[#1c1d20] line-clamp-1">{c.title}</p>
                <p className="text-xs text-zinc-400">{when(c.created_at)}</p>
              </button>
            ))}
          </div>
        </motion.aside>
      )}
    </AnimatePresence>
  );
}
