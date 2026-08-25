import { describe, expect, test } from "vitest";
import { describeFailure } from "./streamError";

// Шлюз кладёт в конверт не только code+message, но и cause (техническую улику:
// ручка, статус, стадия ML) и hint (что чинить). Читать только message значит
// снова свести три разных отказа к одной фразе.

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("describeFailure", () => {
  test("поднимает причину и подсказку из конверта", async () => {
    const failure = await describeFailure(jsonResponse(503, {
      error: {
        code: "db_unavailable",
        message: "Нет связи с базой: connection refused",
        cause: "ML /search → HTTP 503; упало на стадии retrieval",
        hint: "Postgres по адресу db:5432/habitus не отвечает",
      },
    }));

    expect(failure).toEqual({
      code: "db_unavailable",
      message: "Нет связи с базой: connection refused",
      cause: "ML /search → HTTP 503; упало на стадии retrieval",
      hint: "Postgres по адресу db:5432/habitus не отвечает",
    });
  });

  test("конверт без cause/hint остаётся валидным", async () => {
    const failure = await describeFailure(jsonResponse(429, {
      error: { code: "rate_limited", message: "Превышен лимит запросов к ИИ" },
    }));

    expect(failure.code).toBe("rate_limited");
    expect(failure.message).toBe("Превышен лимит запросов к ИИ");
    // Поля нет — значит улики нет. Пустая строка соврала бы, что она есть.
    expect(failure.cause).toBeUndefined();
    expect(failure.hint).toBeUndefined();
  });

  test("не-JSON ответ не роняет разбор", async () => {
    const failure = await describeFailure(
      new Response("<html>502 Bad Gateway</html>", { status: 502 }),
    );

    expect(failure.code).toBe("internal_error");
    expect(failure.message).toContain("502");
  });

  test("429 без тела всё равно называется лимитом", async () => {
    const failure = await describeFailure(new Response("", { status: 429 }));

    expect(failure.code).toBe("rate_limited");
  });
});
