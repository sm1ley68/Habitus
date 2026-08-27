import type { Property } from "@/lib/agent/types";
import { API_BASE } from "./config";

export interface ResultsPage {
  objects: Property[];
  /** Сколько отдано в этой странице. */
  count: number;
  /** Сколько всего сохранено для последнего поиска чата. */
  total: number;
}

/** Страница уже сохранённой выдачи последнего поиска чата («показать ещё»).
 *  Повторный поиск не запускается: весь пул лежит в chat_search_results,
 *  ручка только листает его. */
export async function fetchMoreResults(
  chatId: string, offset: number, limit = 10, signal?: AbortSignal,
): Promise<ResultsPage> {
  const res = await fetch(
    `${API_BASE}/chats/${chatId}/results?offset=${offset}&limit=${limit}`,
    { credentials: "include", signal },
  );
  if (!res.ok) throw new Error(`fetchMoreResults failed: ${res.status}`);
  const body = await res.json();
  return {
    objects: (body.objects ?? []) as Property[],
    count: (body.count as number) ?? 0,
    total: (body.total as number) ?? 0,
  };
}
