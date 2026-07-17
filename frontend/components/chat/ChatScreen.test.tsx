import { render, screen, act, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi, afterEach } from "vitest";
import ChatScreen from "./ChatScreen";
import { useSession } from "@/lib/store/session";

vi.mock("@/lib/api/chats", () => ({
  createChat: vi.fn(async () => ({ id: "c1", title: "Новый чат", created_at: "" })),
}));

beforeEach(() => act(() => useSession.getState().reset()));
afterEach(() => vi.unstubAllGlobals());

// Кадры ровно в том виде, в каком их шлёт Go-шлюз
// (backend/internal/service/search_stream_service.go).
function sseResponse(frames: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      const enc = new TextEncoder();
      for (const f of frames) controller.enqueue(enc.encode(f + "\n\n"));
      controller.close();
    },
  });
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

test("запрос гонит стадии и стримит ответ из SSE-потока бэка", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => sseResponse([
    'event: agent_status\ndata: {"agent":"linguistic","status":"processing","message":"Разбираю запрос…"}',
    'event: text_token\ndata: {"token":"Нашёл "}',
    'event: text_token\ndata: {"token":"варианты"}',
    'event: final_result\ndata: {"objects":[],"suggested_areas_geojson":null,"data_freshness":"2026-07-17"}',
    'event: agent_status\ndata: {"agent":"orchestrator","status":"done","message":""}',
    "event: stream_end\ndata: {}",
  ])));

  render(<ChatScreen />);
  await userEvent.type(screen.getByLabelText("Запрос агенту"), "тихий двор");
  await userEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));

  await waitFor(() => expect(useSession.getState().stage).not.toBe("idle"));
  await waitFor(() => expect(useSession.getState().answer).toBe("Нашёл варианты"));
  await waitFor(() => expect(useSession.getState().chatId).toBe("c1"));
});

test("событие error переводит сессию в состояние ошибки", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => sseResponse([
    'event: error\ndata: {"code":"llm_timeout","message":"Не удалось получить ответ от ИИ"}',
  ])));

  render(<ChatScreen />);
  await userEvent.type(screen.getByLabelText("Запрос агенту"), "тихий двор");
  await userEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));

  await waitFor(() => expect(useSession.getState().stage).toBe("error"));
  expect(useSession.getState().errorMessage).toBe("Не удалось получить ответ от ИИ");
});

test("stage=error renders ErrorState", () => {
  act(() => useSession.setState({ stage: "error" }));
  render(<ChatScreen />);
  expect(screen.getByText("Что-то пошло не так")).toBeInTheDocument();
});
