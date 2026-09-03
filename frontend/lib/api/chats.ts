import type { City } from "@/lib/agent/types";
import { API_BASE } from "./config";

export interface Chat { chat_id: string; city: City; title: string; created_at: string }

// Бэк требует город при создании чата (spb|msk) — без него отвечает 400.
export async function createChat(city: City, title?: string): Promise<Chat> {
  const res = await fetch(`${API_BASE}/chats`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(title ? { city, title } : { city }),
  });
  if (!res.ok) throw new Error(`createChat failed: ${res.status}`);
  return (await res.json()) as Chat;
}

export async function listChats(limit = 20, offset = 0): Promise<Chat[]> {
  const res = await fetch(`${API_BASE}/chats?limit=${limit}&offset=${offset}`, {
    credentials: "include",
  });
  if (!res.ok) throw new Error(`listChats failed: ${res.status}`);
  const body = await res.json();
  return (body.chats ?? []) as Chat[];
}

export interface ChatMessage {
  message_id: string;
  role: "user" | "assistant";
  text: string;
  created_at: string;
  meta?: { suggested_object_ids?: string[] } | null;
}

/** Реплики сохранённого диалога, старые сверху. Нужны, чтобы клик по чату в
 *  истории возвращал разговор, а не только его последнюю выдачу. */
export async function listMessages(
  chatId: string, limit = 50, offset = 0, signal?: AbortSignal,
): Promise<ChatMessage[]> {
  const res = await fetch(
    `${API_BASE}/chats/${chatId}/messages?limit=${limit}&offset=${offset}`,
    { credentials: "include", signal },
  );
  if (!res.ok) throw new Error(`listMessages failed: ${res.status}`);
  const body = await res.json();
  return (body.messages ?? []) as ChatMessage[];
}
