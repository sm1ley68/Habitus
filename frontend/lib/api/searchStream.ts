import type { AgentClient, RunHandlers, RunOptions } from "@/lib/agent/AgentClient";
import type {
  AgentName, AgentEventStatus, Property, GeoZone, ConstraintDiagnostic,
} from "@/lib/agent/types";
import { useSession } from "@/lib/store/session";
import { createChat } from "./chats";
import { API_BASE } from "./config";
import { describeFailure, failureFromEvent, localFailure } from "./streamError";

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
//   final_result  {suggested_areas_geojson,objects,data_freshness,total,has_more,
//                  intent,diagnostics}
//   error         {code,message,cause?,hint?}
//   stream_end    {}
export function createSearchClient(): AgentClient {
  return {
    run(query: string, handlers: RunHandlers, options?: RunOptions) {
      const controller = new AbortController();

      (async () => {
        try {
          // Реплика в уже идущий диалог уходит в ТОТ ЖЕ чат: только так шлюз
          // найдёт разбор предыдущего шага и пришлёт его в ML как prev_parsed
          // (многоходовый чат). Новый чат на каждый запрос означал бы, что
          // «подешевле» разбирается с нуля, без контекста.
          const chatId = options?.chatId
            ?? (await createChat(useSession.getState().city)).chat_id;
          const res = await fetch(`${API_BASE}/chats/${chatId}/messages/stream`, {
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
            handlers.onError?.(await describeFailure(res));
            return;
          }

          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          let properties: Property[] = [];
          let zoneGeoJSON: GeoZone | null = null;
          let areaLabel: string | null = null;
          let total = 0;
          let hasMore = false;
          let diagnostics: ConstraintDiagnostic[] = [];
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
              areaLabel = (f.data.area_label as string) ?? null;
              // total — весь сохранённый пул, а не показанная страница:
              // «показать ещё» дотягивает остаток из GET /chats/{id}/results.
              total = (f.data.total as number) ?? properties.length;
              hasMore = Boolean(f.data.has_more);
              diagnostics = (f.data.diagnostics as ConstraintDiagnostic[]) ?? [];
            } else if (f.event === "error") {
              failed = true;
              handlers.onError?.(failureFromEvent(f.data));
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
          if (!failed) {
            handlers.onDone({
              properties, zoneGeoJSON, areaLabel, chatId, total, hasMore, diagnostics,
            });
          }
        } catch (err) {
          if (controller.signal.aborted) return; // отмена пользователем — молча
          handlers.onError?.(localFailure(err));
        }
      })();

      return () => controller.abort();
    },
  };
}
