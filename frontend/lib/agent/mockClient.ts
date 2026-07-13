import type { AgentClient, RunHandlers } from "./AgentClient";
import type { AgentEvent } from "./types";
import { PROPERTIES, ANSWER_TEXT } from "@/lib/data/mock";

const SCRIPT: AgentEvent[] = [
  { agent: "linguistic", status: "processing", message: "Разбираю запрос…" },
  { agent: "geo", status: "processing", message: "Строю маршруты и считаю расстояния…" },
  { agent: "context", status: "processing", message: "Смотрю на район: шум, экология, инфраструктура…" },
  { agent: "orchestrator", status: "relaxation_triggered", message: "Немного смягчаю критерии, чтобы не потерять хорошие варианты…" },
  { agent: "orchestrator", status: "processing", message: "Собираю ответ…" },
];

export function createMockClient(opts: { speed?: number } = {}): AgentClient {
  const unit = opts.speed ?? 1; // multiplier; 0 disables delays (tests)
  const d = (ms: number) => ms * unit;

  return {
    run(_query: string, handlers: RunHandlers) {
      let cancelled = false;
      const timers: ReturnType<typeof setTimeout>[] = [];
      const wait = (ms: number) =>
        new Promise<void>((res) => timers.push(setTimeout(res, d(ms))));

      (async () => {
        for (const ev of SCRIPT) {
          if (cancelled) return;
          handlers.onEvent(ev);
          await wait(ev.status === "relaxation_triggered" ? 900 : 1100);
        }
        // stream the answer token by token
        const tokens = ANSWER_TEXT.split(/(\s+)/);
        for (const t of tokens) {
          if (cancelled) return;
          handlers.onEvent({ agent: "orchestrator", status: "processing", message: "Собираю ответ…", token: t });
          await wait(28);
        }
        if (cancelled) return;
        handlers.onEvent({ agent: "orchestrator", status: "done", message: "" });
        handlers.onDone({ properties: PROPERTIES });
      })();

      return () => { cancelled = true; timers.forEach(clearTimeout); };
    },
  };
}
