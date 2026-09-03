import type { FavoriteObject } from "@/lib/agent/types";
import { API_BASE } from "./config";

export interface FavoritesPage {
  objects: FavoriteObject[];
  count: number;
  total: number;
}

/** Сохранённые объекты, свежие сверху. Доступно и гостю: после регистрации
 *  сохранённое остаётся при том же пользователе. */
export async function listFavorites(limit = 20, offset = 0): Promise<FavoritesPage> {
  const res = await fetch(`${API_BASE}/favorites?limit=${limit}&offset=${offset}`, {
    credentials: "include",
  });
  if (!res.ok) throw new Error(`listFavorites failed: ${res.status}`);
  const body = await res.json();
  return {
    objects: (body.objects ?? []) as FavoriteObject[],
    count: (body.count as number) ?? 0,
    total: (body.total as number) ?? 0,
  };
}

/** PUT, а не POST: сохранение идемпотентно, повторный клик — то же состояние.
 *  chatId необязателен — объект можно сохранить и с карты, вне подбора. */
export async function addFavorite(objectId: string, chatId?: string | null): Promise<void> {
  const res = await fetch(`${API_BASE}/favorites/${encodeURIComponent(objectId)}`, {
    method: "PUT",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(chatId ? { chat_id: chatId } : {}),
  });
  if (!res.ok) throw new Error(`addFavorite failed: ${res.status}`);
}

export async function removeFavorite(objectId: string): Promise<void> {
  const res = await fetch(`${API_BASE}/favorites/${encodeURIComponent(objectId)}`, {
    method: "DELETE",
    credentials: "include",
  });
  if (!res.ok) throw new Error(`removeFavorite failed: ${res.status}`);
}
