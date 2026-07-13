"use client";
import { useState } from "react";

export default function Composer({ onSubmit }: { onSubmit: (text: string) => void }) {
  const [text, setText] = useState("");
  const submit = () => {
    const t = text.trim();
    if (!t) return;
    onSubmit(t);
    setText("");
  };
  return (
    <form
      onSubmit={(e) => { e.preventDefault(); submit(); }}
      className="w-full max-w-2xl mx-auto flex items-center gap-3 rounded-full border border-zinc-200/80 bg-white px-5 py-3.5 shadow-[0_4px_24px_-10px_rgba(24,24,40,0.14)] transition-colors focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/15"
    >
      <label htmlFor="composer" className="sr-only">Запрос агенту</label>
      <input
        id="composer"
        aria-label="Запрос агенту"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Опишите, какое жильё ищете…"
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="off"
        spellCheck={false}
        enterKeyHint="send"
        data-1p-ignore
        data-lpignore="true"
        className="flex-1 bg-transparent text-[15px] placeholder:text-zinc-400 outline-none focus:outline-none focus-visible:outline-none"
      />
      <button
        type="submit"
        aria-label="Отправить запрос"
        className="grid place-items-center h-9 w-9 rounded-full bg-accent text-white transition active:scale-95 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 19V5M12 5l-6 6M12 5l6 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
    </form>
  );
}
