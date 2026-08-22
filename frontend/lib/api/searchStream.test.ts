import { vi, beforeEach, afterEach, test, expect } from "vitest";
import { createSearchClient, parseSSE } from "./searchStream";
import { useSession } from "@/lib/store/session";

const enc = new TextEncoder();

function sseBody(frames: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(c) {
      for (const f of frames) c.enqueue(enc.encode(f));
      c.close();
    },
  });
}

function frame(event: string, data: unknown): string {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
}

/** Ответ шлюза на POST .../messages/stream. */
function streamOK(frames: string[]): Response {
  return { ok: true, status: 200, body: sseBody(frames) } as unknown as Response;
}

/** Неуспешный ответ с конвертом ошибок шлюза. */
function failure(status: number, body: unknown): Response {
  return {
    ok: false, status, body: null,
    json: async () => body,
  } as unknown as Response;
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  useSession.getState().reset();
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

const run = (query: string, options?: { chatId?: string }) =>
  new Promise<{ code?: string; message?: string; done?: unknown }>((resolve) => {
    createSearchClient().run(
      query,
      {
        onEvent: () => {},
        onDone: (r) => resolve({ done: r }),
        onError: (code, message) => resolve({ code, message }),
      },
      options,
    );
  });

test("реплика в существующий чат не создаёт новый чат", async () => {
  // Многоходовый диалог держится на этом: только запрос в ТОТ ЖЕ чат даст
  // шлюзу найти прошлый разбор и прислать его в ML как prev_parsed.
  fetchMock.mockResolvedValueOnce(streamOK([frame("final_result", { objects: [] })]));

  await run("подешевле", { chatId: "chat-42" });

  const urls = fetchMock.mock.calls.map((c) => String(c[0]));
  expect(urls.some((u) => u.endsWith("/chats"))).toBe(false); // createChat не звался
  expect(urls[0]).toContain("/chats/chat-42/messages/stream");
});

test("первый запрос заводит чат", async () => {
  fetchMock
    .mockResolvedValueOnce({ ok: true, json: async () => ({ chat_id: "new-1", city: "msk" }) } as unknown as Response)
    .mockResolvedValueOnce(streamOK([frame("final_result", { objects: [] })]));

  const res = await run("тихая двушка");

  expect(String(fetchMock.mock.calls[0][0])).toContain("/chats");
  expect(String(fetchMock.mock.calls[1][0])).toContain("/chats/new-1/messages/stream");
  expect((res.done as { chatId: string }).chatId).toBe("new-1");
});

test("429 доносит честное сообщение шлюза, а не «внутреннюю ошибку»", async () => {
  fetchMock.mockResolvedValueOnce(failure(429, {
    error: { code: "rate_limited", message: "Превышен лимит запросов к ИИ (30 в час). Попробуйте снова через 12 мин." },
  }));

  const res = await run("тихая двушка", { chatId: "c1" });

  expect(res.code).toBe("rate_limited");
  expect(res.message).toContain("12 мин");
});

test("429 без разбираемого тела всё равно не выдаётся за внутреннюю ошибку", async () => {
  fetchMock.mockResolvedValueOnce({
    ok: false, status: 429, body: null,
    json: async () => { throw new Error("not json"); },
  } as unknown as Response);

  const res = await run("тихая двушка", { chatId: "c1" });

  expect(res.code).toBe("rate_limited");
});

test("total/has_more/diagnostics доезжают из final_result", async () => {
  fetchMock.mockResolvedValueOnce(streamOK([
    frame("final_result", {
      objects: [{ id: "A" }],
      total: 30,
      has_more: true,
      diagnostics: [{ constraint: "цена", remaining: 0 }],
    }),
  ]));

  const res = await run("двушка", { chatId: "c1" });
  const done = res.done as { total: number; hasMore: boolean; diagnostics: unknown[] };

  expect(done.total).toBe(30);
  expect(done.hasMore).toBe(true);
  expect(done.diagnostics).toHaveLength(1);
});

test("final_result без новых полей не ломает старый путь", async () => {
  // Поля аддитивные: ответ без них обязан отработать как раньше.
  fetchMock.mockResolvedValueOnce(streamOK([frame("final_result", { objects: [{ id: "A" }] })]));

  const res = await run("двушка", { chatId: "c1" });
  const done = res.done as { total: number; hasMore: boolean; diagnostics: unknown[] };

  expect(done.total).toBe(1); // фолбэк — сколько объектов пришло
  expect(done.hasMore).toBe(false);
  expect(done.diagnostics).toEqual([]);
});

test("parseSSE переживает битый кадр", () => {
  expect(parseSSE("event: x\ndata: {не json")).toBeNull();
});
