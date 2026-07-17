import type { ObjectPassport } from "@/lib/agent/types";
import { API_BASE } from "./config";

// Паспорт объекта. Форма ответа GET /objects/{id} совпадает с ObjectPassport
// поле-в-поле (контракт §4 + Н.1–Н.3) — маппинга нет намеренно.
export async function getObjectPassport(
  objectId: string,
  chatId?: string,
): Promise<ObjectPassport> {
  const qs = chatId ? `?chat_id=${encodeURIComponent(chatId)}` : "";
  const res = await fetch(`${API_BASE}/objects/${encodeURIComponent(objectId)}${qs}`, {
    credentials: "include",
  });
  if (!res.ok) throw new Error(`getObjectPassport failed: ${res.status}`);
  return (await res.json()) as ObjectPassport;
}
