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
