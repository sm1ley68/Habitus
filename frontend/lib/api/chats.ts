import { API_BASE } from "./config";

export interface Chat { id: string; title: string; created_at: string }

export async function createChat(title?: string): Promise<Chat> {
  const res = await fetch(`${API_BASE}/chats`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(title ? { title } : {}),
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
