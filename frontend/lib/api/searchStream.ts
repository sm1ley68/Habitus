import type { AgentClient, RunHandlers } from "@/lib/agent/AgentClient";
import type {
  AgentName, AgentEventStatus, Property, GeoZone,
} from "@/lib/agent/types";
import { useSession } from "@/lib/store/session";
import { createChat } from "./chats";
import { API_BASE } from "./config";

export interface SSEFrame { event: string; data: Record<string, unknown> }

// Разбирает один кадр SSE ("event:" + одна или несколько "data:" строк).
// Битый JSON — не исключение: кадр пропускается, поток живёт дальше.
export function parseSSE(frame: string): SSEFrame | null {
  let event = "message";
  const dataLines: string[] = [];
  for (const raw of frame.split("\n")) {
    const line = raw.trimEnd();
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
  }
  if (!dataLines.length) return null;
  try {
    return { event, data: JSON.parse(dataLines.join("\n")) };
  } catch {
    return null;
  }
}

// Реальный поисковый клиент: создаёт чат, затем читает SSE-поток шлюза.
// События (backend/internal/service/search_stream_service.go):
//   agent_status  {agent,status,message}
//   text_token    {token}
//   chat_renamed  {chat_id,title}
//   final_result  {suggested_areas_geojson,objects,data_freshness}
//   error         {code,message}
//   stream_end    {}
export function createSearchClient(): AgentClient {
  return {
    run(query: string, handlers: RunHandlers) {
      const controller = new AbortController();

      (async () => {
        try {
          const chat = await createChat(useSession.getState().city);
          const res = await fetch(`${API_BASE}/chats/${chat.chat_id}/messages/stream`, {
            method: "POST",
            credentials: "include",
            headers: {
              "Content-Type": "application/json",
              Accept: "text/event-stream",
            },
            body: JSON.stringify({ text: query }),
            signal: controller.signal,
          });

          if (!res.ok || !res.body) {
            handlers.onError?.("internal_error", `Не удалось начать поток (${res.status})`);
            return;
          }

          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          let properties: Property[] = [];
          let zoneGeoJSON: GeoZone | null = null;
          let failed = false;

          const handle = (f: SSEFrame) => {
            if (f.event === "agent_status") {
              handlers.onEvent({
                agent: f.data.agent as AgentName,
                status: f.data.status as AgentEventStatus,
                message: (f.data.message as string) ?? "",
              });
            } else if (f.event === "text_token") {
              handlers.onEvent({
                agent: "orchestrator",
                status: "processing",
                message: "",
                token: (f.data.token as string) ?? "",
              });
            } else if (f.event === "final_result") {
              properties = (f.data.objects as Property[]) ?? [];
              zoneGeoJSON = (f.data.suggested_areas_geojson as GeoZone) ?? null;
            } else if (f.event === "error") {
              failed = true;
              handlers.onError?.(
                (f.data.code as string) ?? "internal_error",
                (f.data.message as string) ?? "Ошибка потока",
              );
            }
          };

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
              if (parsed) handle(parsed);
            }
          }

          // После error поток уже закрыт как неуспешный — не рапортуем «готово».
          if (!failed) handlers.onDone({ properties, zoneGeoJSON, chatId: chat.chat_id });
        } catch (err) {
          if (controller.signal.aborted) return; // отмена пользователем — молча
          handlers.onError?.(
            "internal_error",
            err instanceof Error ? err.message : "Сетевая ошибка",
          );
        }
      })();

      return () => controller.abort();
    },
  };
}
