// Общий разбор неуспешного HTTP-ответа для потоковых ручек.
//
// Шлюз кладёт в тело честное объяснение (единый конверт
// `{"error":{"code","message"}}`, см.
// backend/internal/http/middleware/errorenvelope.go), и на 429 это
// «Превышен лимит запросов к ИИ (30 в час). Попробуйте снова через N мин.» —
// выбрасывать его и показывать «внутреннюю ошибку» значит врать пользователю
// про причину.
export async function describeFailure(res: Response): Promise<[string, string]> {
  try {
    const body = await res.json();
    const err = body?.error ?? body;
    const code = typeof err?.code === "string" ? err.code : null;
    const message = typeof err?.message === "string" ? err.message : null;
    if (code || message) {
      return [
        code ?? (res.status === 429 ? "rate_limited" : "internal_error"),
        message ?? `Не удалось начать поток (${res.status})`,
      ];
    }
  } catch {
    // не JSON — падаем в общий текст ниже
  }
  if (res.status === 429) {
    return ["rate_limited", "Слишком много запросов к ИИ. Попробуйте чуть позже."];
  }
  return ["internal_error", `Не удалось начать поток (${res.status})`];
}
