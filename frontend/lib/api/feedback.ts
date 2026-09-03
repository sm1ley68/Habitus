import type { FeedbackVerdict } from "@/lib/agent/types";
import { API_BASE } from "./config";

/** Оценка объекта из выдачи — единственный продакшн-сигнал о качестве подбора.
 *  Повторная отправка перезаписывает оценку: пользователь может передумать.
 *  Оценить можно только объект, который был в выдаче этого чата. */
export async function saveFeedback(
  chatId: string, objectId: string, verdict: FeedbackVerdict, reason?: string,
): Promise<void> {
  const res = await fetch(
    `${API_BASE}/chats/${chatId}/results/${encodeURIComponent(objectId)}/feedback`,
    {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(reason ? { verdict, reason } : { verdict }),
    },
  );
  if (!res.ok) throw new Error(`saveFeedback failed: ${res.status}`);
}
