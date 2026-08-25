// Общий разбор неуспешного HTTP-ответа для потоковых ручек.
//
// Шлюз кладёт в тело честное объяснение (единый конверт
// `{"error":{"code","message","cause?","hint?"}}`, см.
// backend/internal/http/middleware/errorenvelope.go), и на 429 это
// «Превышен лимит запросов к ИИ (30 в час). Попробуйте снова через N мин.» —
// выбрасывать его и показывать «внутреннюю ошибку» значит врать пользователю
// про причину.
//
// cause/hint аддитивны: cause — техническая улика (какая ручка ML, какой
// статус, на какой стадии упало), hint — что с этим делать. Их отсутствие
// означает «улики нет»; подставлять вместо них пустую строку нельзя — экран
// тогда нарисует пустой блок там, где сказать нечего.

export interface StreamFailure {
  code: string;
  message: string;
  cause?: string;
  hint?: string;
}

function optionalText(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

export async function describeFailure(res: Response): Promise<StreamFailure> {
  try {
    const body = await res.json();
    const err = body?.error ?? body;
    const code = typeof err?.code === "string" ? err.code : null;
    const message = typeof err?.message === "string" ? err.message : null;
    if (code || message) {
      return {
        code: code ?? (res.status === 429 ? "rate_limited" : "internal_error"),
        message: message ?? `Не удалось начать поток (${res.status})`,
        cause: optionalText(err?.cause),
        hint: optionalText(err?.hint),
      };
    }
  } catch {
    // не JSON — падаем в общий текст ниже
  }
  if (res.status === 429) {
    return {
      code: "rate_limited",
      message: "Слишком много запросов к ИИ. Попробуйте чуть позже.",
    };
  }
  return {
    code: "internal_error",
    message: `Не удалось начать поток (${res.status})`,
  };
}

/** Разбор кадра SSE-события `error` — та же форма, что у REST-конверта. */
export function failureFromEvent(data: Record<string, unknown>): StreamFailure {
  return {
    code: (data.code as string) ?? "internal_error",
    message: (data.message as string) ?? "Ошибка потока",
    cause: optionalText(data.cause),
    hint: optionalText(data.hint),
  };
}

/** Отказ, случившийся на нашей стороне: сеть, разорванное соединение, парсер. */
export function localFailure(err: unknown): StreamFailure {
  return {
    code: "internal_error",
    message: err instanceof Error ? err.message : "Сетевая ошибка",
  };
}
