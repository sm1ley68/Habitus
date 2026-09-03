import { API_BASE } from "./config";

// is_guest приходит из всех auth-ручек, а не только из /auth/guest: фронту он
// нужен на каждом входе, чтобы решать, показывать ли призыв зарегистрироваться
// (контракт §1, «Гостевая сессия»).
export interface User { id: string; email: string; name: string; is_guest: boolean }

// Go-шлюз заворачивает ошибки в конверт {error:{code,message}}
// (backend/internal/http/middleware/errorenvelope.go). Достаём message, чтобы
// показать пользователю текст бэка, а не голый "HTTP 401".
async function errorMessage(res: Response, fallback: string): Promise<string> {
  try {
    const body = await res.json();
    return body?.error?.message ?? fallback;
  } catch {
    return fallback;
  }
}

function post(path: string, body: unknown): Promise<Response> {
  return fetch(`${API_BASE}${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export async function register(email: string, password: string, name: string): Promise<User> {
  const res = await post("/auth/register", { email, password, name });
  if (!res.ok) throw new Error(await errorMessage(res, "Не удалось зарегистрироваться"));
  return (await res.json()) as User;
}

export async function login(email: string, password: string): Promise<User> {
  const res = await post("/auth/login", { email, password });
  if (!res.ok) throw new Error(await errorMessage(res, "Не удалось войти"));
  return (await res.json()) as User;
}

// Сессия без регистрации — под первый поиск. Ручка идемпотентна: при живой
// куке новый гость не заводится, приходит текущий пользователь (в том числе
// зарегистрированный), поэтому её безопасно звать на старте приложения.
export async function guest(): Promise<User> {
  const res = await post("/auth/guest", {});
  if (!res.ok) throw new Error(await errorMessage(res, "Не удалось начать сессию"));
  return (await res.json()) as User;
}

export async function logout(): Promise<void> {
  await fetch(`${API_BASE}/auth/logout`, { method: "POST", credentials: "include" });
}

// Отсутствие сессии — это не ошибка, а штатное состояние «ещё не вошли».
export async function me(): Promise<User | null> {
  const res = await fetch(`${API_BASE}/me`, { credentials: "include" });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(await errorMessage(res, "Не удалось проверить сессию"));
  return (await res.json()) as User;
}
