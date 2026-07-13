import { USE_MOCK, API_BASE } from "./config";

// The streaming data-seam for the per-object Q&A chat (contract §Н.6).
// Components call createObjectChatClient().ask(...) instead of touching fetch or
// the mock directly, so mock↔backend is a single config flip
// (NEXT_PUBLIC_USE_MOCK=false) with no UI changes. The SSE event names/shapes
// mirror the search engine (§3): text_token / error / stream_end.

export interface ObjectChatHandlers {
  onToken(token: string): void;
  onDone(): void;
  onError(code: string, message: string): void;
}

export interface ObjectChatClient {
  /** Starts one ask; returns a cancel function that aborts the in-flight stream. */
  ask(
    objectId: string,
    chatId: string,
    text: string,
    handlers: ObjectChatHandlers,
  ): () => void;
}

// --- Mock: stream a grounded canned answer token-by-token ---
// Mirrors lib/agent/mockClient.ts: a setTimeout loop at ~28ms/token, returning a
// cancel that clears pending timers. The answer echoes the question topic so the
// dock feels responsive, and stays framed as "grounded in this dossier".
function mockAnswer(text: string): string {
  const q = text.trim().replace(/\s+/g, " ");
  const topic = q.length > 60 ? q.slice(0, 57).trimEnd() + "…" : q;
  return (
    `По вашему вопросу «${topic}» — вот что говорит досье этого объекта. ` +
    `Я свёл вместе проверенные слои города: логистику семьи, социальный слой ` +
    `в радиусе двора и инсоляцию окон. Коротко: критичные для вас критерии ` +
    `выполнены, а по спорным пунктам ниже честно показан компромисс. ` +
    `Если данных по объекту недостаточно, я не додумываю — так и пишу.`
  );
}

function createMockClient(unit = 1): ObjectChatClient {
  const d = (ms: number) => ms * unit;
  return {
    ask(_objectId, _chatId, text, handlers) {
      let cancelled = false;
      const timers: ReturnType<typeof setTimeout>[] = [];
      const wait = (ms: number) =>
        new Promise<void>((res) => timers.push(setTimeout(res, d(ms))));

      (async () => {
        const tokens = mockAnswer(text).split(/(\s+)/);
        for (const t of tokens) {
          if (cancelled) return;
          handlers.onToken(t);
          await wait(28);
        }
        if (cancelled) return;
        handlers.onDone();
      })();

      return () => {
        cancelled = true;
        timers.forEach(clearTimeout);
      };
    },
  };
}

// --- Real: POST the SSE endpoint and parse text/event-stream frames ---
function createFetchClient(): ObjectChatClient {
  return {
    ask(objectId, chatId, text, handlers) {
      const controller = new AbortController();

      (async () => {
        try {
          const res = await fetch(
            `${API_BASE}/objects/${objectId}/ask/stream`,
            {
              method: "POST",
              credentials: "include",
              headers: {
                "Content-Type": "application/json",
                Accept: "text/event-stream",
              },
              body: JSON.stringify({ text, chat_id: chatId }),
              signal: controller.signal,
            },
          );

          if (!res.ok || !res.body) {
            handlers.onError(
              "internal_error",
              `Не удалось начать поток (${res.status})`,
            );
            return;
          }

          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";

          // Parse one complete SSE frame ("event:" + "data:" lines).
          const handleFrame = (frame: string) => {
            let event = "message";
            const dataLines: string[] = [];
            for (const raw of frame.split("\n")) {
              const line = raw.trimEnd();
              if (line.startsWith("event:")) event = line.slice(6).trim();
              else if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
            }
            const data = dataLines.join("\n");

            if (event === "text_token") {
              try {
                handlers.onToken(JSON.parse(data).token ?? "");
              } catch {
                /* ignore malformed token frame */
              }
            } else if (event === "error") {
              let code = "internal_error";
              let message = "Ошибка потока";
              try {
                const p = JSON.parse(data);
                code = p.code ?? code;
                message = p.message ?? message;
              } catch {
                /* keep defaults */
              }
              handlers.onError(code, message);
            } else if (event === "stream_end") {
              handlers.onDone();
            }
          };

          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            // Frames are separated by a blank line.
            let sep: number;
            while ((sep = buffer.indexOf("\n\n")) !== -1) {
              const frame = buffer.slice(0, sep);
              buffer = buffer.slice(sep + 2);
              if (frame.trim()) handleFrame(frame);
            }
          }
        } catch (err) {
          if (controller.signal.aborted) return; // user cancelled — silent
          handlers.onError(
            "internal_error",
            err instanceof Error ? err.message : "Сетевая ошибка",
          );
        }
      })();

      return () => controller.abort();
    },
  };
}

export function createObjectChatClient(): ObjectChatClient {
  return USE_MOCK ? createMockClient() : createFetchClient();
}
