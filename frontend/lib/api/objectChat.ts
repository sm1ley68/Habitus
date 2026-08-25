import { parseSSE } from "./searchStream";
import { API_BASE } from "./config";
import { describeFailure, failureFromEvent, localFailure } from "./streamError";
import type { StreamFailure } from "./streamError";

// Чат по конкретному объекту (контракт §Н.6). Имена SSE-событий те же, что у
// поискового движка: text_token / error / stream_end.

export interface ObjectChatHandlers {
  onToken(token: string): void;
  onDone(): void;
  onError(failure: StreamFailure): void;
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

export function createObjectChatClient(): ObjectChatClient {
  return {
    ask(objectId, chatId, text, handlers) {
      const controller = new AbortController();

      (async () => {
        try {
          const res = await fetch(
            `${API_BASE}/objects/${encodeURIComponent(objectId)}/ask/stream`,
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
            handlers.onError(await describeFailure(res));
            return;
          }

          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";

          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            let sep: number;
            while ((sep = buffer.indexOf("\n\n")) !== -1) {
              const frame = buffer.slice(0, sep);
              buffer = buffer.slice(sep + 2);
              if (!frame.trim()) continue;
              const parsed = parseSSE(frame);
              if (!parsed) continue;

              if (parsed.event === "text_token") {
                handlers.onToken((parsed.data.token as string) ?? "");
              } else if (parsed.event === "error") {
                handlers.onError(failureFromEvent(parsed.data));
              } else if (parsed.event === "stream_end") {
                handlers.onDone();
              }
            }
          }
        } catch (err) {
          if (controller.signal.aborted) return; // отмена пользователем — молча
          handlers.onError(localFailure(err));
        }
      })();

      return () => controller.abort();
    },
  };
}
