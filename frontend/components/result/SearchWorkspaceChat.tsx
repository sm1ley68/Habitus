"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { createSearchClient } from "@/lib/api/searchStream";
import { useSession } from "@/lib/store/session";

export default function SearchWorkspaceChat() {
  const messages = useSession((state) => state.searchMessages);
  const answer = useSession((state) => state.answer);
  const updating = useSession((state) => state.searchUpdating);
  const refine = useSession((state) => state.refineQuery);
  const chatId = useSession((state) => state.chatId);
  const client = useMemo(() => createSearchClient(), []);
  const [text, setText] = useState("");
  const thread = useRef<HTMLDivElement>(null);

  useEffect(() => {
    thread.current?.scrollTo({ top: thread.current.scrollHeight, behavior: "smooth" });
  }, [messages.length, answer]);

  const submit = () => {
    const query = text.trim();
    if (!query || updating || !chatId) return;
    refine(client, query);
    setText("");
  };

  return (
    <section className="order-3 flex min-h-[320px] flex-col overflow-hidden rounded-3xl border border-zinc-200 bg-white xl:min-h-0">
      <header className="border-b border-zinc-100 px-4 py-4">
        <div className="flex items-center gap-2">
          <span aria-hidden className="grid h-7 w-7 place-items-center rounded-xl bg-accent/10 text-accent">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
              <path d="M8 10h8M8 14h5M6 20l-2 2v-5a8 8 0 118 3H6z" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </span>
          <h2 className="text-sm font-medium tracking-tight text-[#1c1d20]">Уточнить поиск</h2>
        </div>
        <p className="mt-2 text-xs leading-5 text-zinc-400">
          Сообщение обновит критерии, список квартир и карту.
        </p>
      </header>

      <div ref={thread} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-3 py-4">
        {messages.length === 0 && (
          <p className="rounded-2xl bg-[#f7f6ff] px-3.5 py-3 text-xs leading-5 text-[#6c65a8]">
            Например: «Покажи варианты потише и ближе к парку».
          </p>
        )}
        {messages.map((message) => (
          <div
            key={message.id}
            className={`max-w-[92%] rounded-2xl px-3.5 py-2.5 text-[13px] leading-5 ${
              message.role === "user"
                ? "ml-auto rounded-br-md bg-accent text-white"
                : "mr-auto rounded-bl-md bg-zinc-100 text-zinc-700"
            }`}
          >
            {message.text}
          </div>
        ))}
        {updating && (
          <div className="mr-auto max-w-[92%] rounded-2xl rounded-bl-md bg-[#f7f6ff] px-3.5 py-2.5 text-[13px] leading-5 text-[#5f55b8]">
            {answer || (
              <span className="inline-flex items-center gap-2">
                <span aria-hidden className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-accent/25 border-t-accent" />
                Обновляю подборку…
              </span>
            )}
            {answer && <span className="ml-1 inline-block h-[1em] w-[2px] animate-pulse align-[-2px] bg-accent" />}
          </div>
        )}
      </div>

      <form
        onSubmit={(event) => { event.preventDefault(); submit(); }}
        className="border-t border-zinc-100 p-3"
      >
        <div className="flex items-end gap-2 rounded-2xl border border-zinc-200 bg-white p-2 shadow-[0_8px_28px_-20px_rgba(28,29,32,0.45)] transition focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/10">
          <label htmlFor="workspace-chat-input" className="sr-only">Уточнить критерии поиска</label>
          <textarea
            id="workspace-chat-input"
            aria-label="Уточнить критерии поиска"
            rows={2}
            value={text}
            disabled={updating || !chatId}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                submit();
              }
            }}
            placeholder="Уточните условия…"
            className="min-h-11 flex-1 resize-none bg-transparent px-1.5 py-1 text-[13px] leading-5 text-zinc-700 outline-none placeholder:text-zinc-400 disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={!text.trim() || updating || !chatId}
            aria-label="Отправить уточнение"
            className="grid h-9 w-9 shrink-0 place-items-center rounded-xl bg-accent text-white transition active:scale-95 disabled:cursor-not-allowed disabled:opacity-35"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden>
              <path d="M12 19V5M12 5l-6 6M12 5l6 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
        </div>
      </form>
    </section>
  );
}

