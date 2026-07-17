"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { motion, AnimatePresence, useReducedMotion } from "framer-motion";
import { SPRING } from "@/lib/motion";
import { createObjectChatClient } from "@/lib/api/objectChat";
import type { ObjectPassport, LifestyleBlock } from "@/lib/agent/types";

// The per-object Q&A dock (contract §Н.6). A compact "ask about this object"
// conversation grounded in the dossier: suggested-question chips derived from
// the passport, a message thread, and a composer that streams the assistant's
// answer token-by-token through the createObjectChatClient() seam (mock today,
// реальный SSE через POST /objects/{id}/ask/stream). One in-flight request at a time; the
// composer is blocked during a stream, mirroring the app's stream-lock rule.

interface Msg {
  id: string;
  role: "user" | "assistant";
  text: string;
}

// Short topic word per hero/compromise block key, used to phrase chips.
const BLOCK_TOPIC: Record<string, string> = {
  view_and_climate: "свету",
  social_environment: "окружению",
  family_routing: "логистике",
};

// Derive 3–4 grounded suggested questions from the passport: compromises first
// (the honest tension points), then hero blocks (the "wow" chapters). Deduped
// and capped so the chip row stays a single tidy line-wrap.
function suggestedQuestions(passport: ObjectPassport): string[] {
  const a = passport.lifestyle_analysis;
  const out: string[] = [];

  for (const c of a.compromises ?? []) {
    const topic = BLOCK_TOPIC[c.block_key];
    out.push(topic ? `Почему компромисс по ${topic}?` : "В чём именно компромисс?");
  }

  const heroes = a.blocks.filter((b) => b.tier === "hero");
  for (const b of heroes) {
    out.push(heroQuestion(b));
  }

  // Fallback so the dock is never empty even for a sparse dossier.
  if (out.length === 0) {
    out.push("Кому подойдёт эта квартира?", "Какие здесь главные компромиссы?");
  }

  return [...new Set(out)].slice(0, 4);
}

function heroQuestion(b: LifestyleBlock): string {
  switch (b.key) {
    case "family_routing":
      return "Насколько безопасен путь в школу?";
    case "social_environment":
      return "Что с окружением и соседями рядом?";
    case "view_and_climate":
      return "Сколько солнца в окнах зимой?";
    default:
      return `Расскажи подробнее: ${b.title.toLowerCase()}?`;
  }
}

export default function PassportChat({
  objectId,
  chatId,
  passport,
}: {
  objectId: string;
  chatId: string;
  passport: ObjectPassport;
}) {
  const reduce = useReducedMotion();
  const [messages, setMessages] = useState<Msg[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");

  const cancelRef = useRef<(() => void) | null>(null);
  const clientRef = useRef<ReturnType<typeof createObjectChatClient> | null>(null);
  if (!clientRef.current) clientRef.current = createObjectChatClient();

  const chips = useMemo(() => suggestedQuestions(passport), [passport]);

  // Tear down any live stream if the dock unmounts (leaving the passport).
  useEffect(() => () => cancelRef.current?.(), []);

  const ask = useCallback(
    (text: string) => {
      const q = text.trim();
      if (!q || streaming) return; // empty ignored; one in-flight at a time

      setError(null);
      const userMsg: Msg = { id: `u-${Date.now()}`, role: "user", text: q };
      const answerId = `a-${Date.now()}`;
      setMessages((m) => [
        ...m,
        userMsg,
        { id: answerId, role: "assistant", text: "" },
      ]);
      setStreaming(true);

      const setAnswer = (fn: (prev: string) => string) =>
        setMessages((m) =>
          m.map((msg) =>
            msg.id === answerId ? { ...msg, text: fn(msg.text) } : msg,
          ),
        );

      cancelRef.current = clientRef.current!.ask(objectId, chatId, q, {
        onToken: (token) => setAnswer((prev) => prev + token),
        onDone: () => {
          setStreaming(false);
          cancelRef.current = null;
        },
        onError: (_code, message) => {
          setStreaming(false);
          cancelRef.current = null;
          setError(message || "Не удалось получить ответ. Попробуйте ещё раз.");
          // Drop the empty assistant bubble so the thread stays clean.
          setMessages((m) => m.filter((msg) => !(msg.id === answerId && !msg.text)));
        },
      });
    },
    [objectId, chatId, streaming],
  );

  const stop = useCallback(() => {
    cancelRef.current?.();
    cancelRef.current = null;
    setStreaming(false);
  }, []);

  const submit = () => {
    ask(draft);
    setDraft("");
  };

  return (
    <section
      aria-label="Спросить про объект"
      className="border-t border-zinc-100 bg-white"
    >
      <div className="mx-auto w-full max-w-5xl px-6 py-14">
        {/* Header — asymmetric: eyebrow + object name on the left, no card. */}
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 className="text-[11px] font-medium uppercase tracking-[0.2em] text-zinc-400">
            Спросить про объект
          </h2>
          <span className="text-sm text-zinc-500">{passport.name}</span>
        </div>
        <p className="mt-2 max-w-[54ch] text-sm leading-relaxed text-zinc-500">
          Отвечаю только по данным этого досье и слоям города. Чего в данных нет —
          так и скажу, без домыслов.
        </p>

        {/* Suggested-question chips. */}
        <motion.ul
          initial="hidden"
          whileInView="show"
          viewport={{ once: true, margin: "-40px" }}
          variants={{ show: { transition: { staggerChildren: reduce ? 0 : 0.05 } } }}
          className="mt-5 flex flex-wrap gap-2.5"
        >
          {chips.map((chip, i) => (
            <motion.li
              key={`${chip}-${i}`}
              variants={{
                hidden: reduce ? {} : { opacity: 0, y: 8 },
                show: { opacity: 1, y: 0 },
              }}
              transition={SPRING.soft}
            >
              <button
                type="button"
                onClick={() => ask(chip)}
                disabled={streaming}
                className="inline-flex items-center gap-2 rounded-full border border-[#7C8CFF]/30 bg-[#7C8CFF]/[0.06] px-3.5 py-1.5 text-sm text-[#1c1d20] transition-transform hover:-translate-y-px hover:bg-[#7C8CFF]/[0.1] active:scale-[0.98] disabled:cursor-not-allowed disabled:opacity-45 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke="#5b6bd6"
                  strokeWidth="1.6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                  className="shrink-0"
                >
                  <path d="M6 6a2 2 0 1 1 2.6 1.9c-.6.2-.9.6-.9 1.3v.4M8 12v.2" />
                </svg>
                {chip}
              </button>
            </motion.li>
          ))}
        </motion.ul>

        {/* Message thread — labelled log region, polite live updates. */}
        {messages.length > 0 && (
          <div
            role="log"
            aria-label="Диалог по объекту"
            aria-live="polite"
            className="mt-8 flex flex-col gap-5"
          >
            <AnimatePresence initial={false}>
              {messages.map((m) => (
                <motion.div
                  key={m.id}
                  initial={reduce ? false : { opacity: 0, y: 8 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={SPRING.soft}
                  className={m.role === "user" ? "flex justify-end" : "flex justify-start"}
                >
                  {m.role === "user" ? (
                    <p className="max-w-[80%] rounded-2xl rounded-br-md bg-[#1c1d20] px-4 py-2.5 text-[15px] leading-relaxed text-white">
                      {m.text}
                    </p>
                  ) : (
                    <p className="max-w-[80%] text-[15px] leading-relaxed text-[#1c1d20]">
                      {m.text}
                      {streaming && m.id === messages[messages.length - 1]?.id && (
                        <TypingCursor reduce={!!reduce} empty={!m.text} />
                      )}
                    </p>
                  )}
                </motion.div>
              ))}
            </AnimatePresence>
          </div>
        )}

        {/* Inline error — re-enables input, offers no dead ends. */}
        {error && (
          <p role="alert" className="mt-5 text-sm text-rose-600">
            {error}
          </p>
        )}

        {/* Composer — reused styling from components/chat/Composer.tsx, adapted
            to block during a stream and swap the send glyph for a stop control. */}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
          className="mt-6 flex items-center gap-3 rounded-full border border-zinc-200/80 bg-white px-5 py-3.5 shadow-[0_4px_24px_-10px_rgba(24,24,40,0.14)] transition-colors focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/15"
        >
          <label htmlFor="passport-chat-input" className="sr-only">
            Вопрос про объект {passport.name}
          </label>
          <input
            id="passport-chat-input"
            aria-label={`Вопрос про объект ${passport.name}`}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={streaming ? "Отвечаю…" : "Спросите про этот объект…"}
            disabled={streaming}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            spellCheck={false}
            enterKeyHint="send"
            data-1p-ignore
            data-lpignore="true"
            className="flex-1 bg-transparent text-[15px] placeholder:text-zinc-400 outline-none focus:outline-none focus-visible:outline-none disabled:text-zinc-400"
          />
          {streaming ? (
            <button
              type="button"
              onClick={stop}
              aria-label="Остановить ответ"
              className="grid h-9 w-9 place-items-center rounded-full bg-zinc-200 text-[#1c1d20] transition active:scale-95 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
                <rect x="4" y="4" width="8" height="8" rx="1.5" />
              </svg>
            </button>
          ) : (
            <button
              type="submit"
              aria-label="Отправить вопрос"
              disabled={!draft.trim()}
              className="grid h-9 w-9 place-items-center rounded-full bg-accent text-white transition active:scale-95 disabled:cursor-not-allowed disabled:opacity-40 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <path
                  d="M12 19V5M12 5l-6 6M12 5l6 6"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
          )}
        </form>
      </div>
    </section>
  );
}

// Streaming affordance. With motion: a blinking accent caret (or three-dot
// "typing" while the answer is still empty). Under reduced motion: text simply
// appears, no caret animation — a static dot stands in only for the empty wait.
function TypingCursor({ reduce, empty }: { reduce: boolean; empty: boolean }) {
  if (reduce) {
    return empty ? (
      <span className="ml-0.5 align-[-1px] text-zinc-400">…</span>
    ) : null;
  }
  if (empty) {
    return (
      <span className="ml-1 inline-flex gap-1 align-[1px]" aria-hidden="true">
        {[0, 1, 2].map((i) => (
          <motion.span
            key={i}
            className="h-1.5 w-1.5 rounded-full bg-accent/70"
            animate={{ opacity: [0.25, 1, 0.25] }}
            transition={{ duration: 1, repeat: Infinity, delay: i * 0.16, ease: "easeInOut" }}
          />
        ))}
      </span>
    );
  }
  return (
    <span
      aria-hidden="true"
      className="ml-0.5 inline-block h-[1.1em] w-[2px] align-[-2px] bg-accent animate-pulse"
    />
  );
}
