# Подключение фронта к бэку — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Довести проект до состояния, где единственное недостающее для полностью рабочего приложения — это `.env` с ключами.

**Architecture:** Бэк уже готов целиком: Go-шлюз (`backend/`, :8080) отдаёт `/api/v1` ровно в формах, которых ждёт фронт, ML-сервис (`habitus/`, :8000) даёт search/dossier/object-ask. Фронт написан на data-seam (`lib/api/`), где паспорт и чат-по-объекту уже имеют реальные fetch-реализации. Не хватает трёх швов: авторизации, SSE-клиента поиска и гео-слоёв. Плюс проксирование `/api/v1` через Next.js rewrites — чтобы куки были same-origin и `NEXT_PUBLIC_API_BASE` остался дефолтным `/api/v1`.

**Tech Stack:** Next.js 15 (App Router) + React 19 + zustand + vitest; Go/Fiber; FastAPI; Postgres 16 + PostGIS + pgvector; Docker Compose.

## Global Constraints

- Координаты **везде** `[lng, lat]`, WGS84 (EPSG:4326). Без трансформаций на фронте.
- Не выдумывать факты о городе. Нет данных → честная деградация, не синтетический ноль.
- Секреты не коммитить. `.env` в `.gitignore`.
- Коммиты: Conventional Commits на русском, без трейлеров и подписей.
- Работа напрямую в `main`.
- Все `/api/v1` роуты, кроме `/auth/register` и `/auth/login`, закрыты `authMw` — любой клиентский fetch обязан слать `credentials: "include"`.
- Сессионная кука: `habitus_session`, `HTTPOnly`, `SameSite=Lax`, `Path=/`.
- Тесты: `cd frontend && npm test`, `cd backend && go test ./...`, `uv run pytest`.

---

### Task 1: Прокси `/api/v1` через Next.js rewrites

Куки становятся same-origin, `NEXT_PUBLIC_API_BASE` остаётся дефолтным `/api/v1`, CORS не участвует.

**Files:**
- Modify: `frontend/next.config.mjs`
- Create: `frontend/.env.local.example`

**Interfaces:**
- Produces: браузерный путь `/api/v1/*` → `${BACKEND_ORIGIN}/api/v1/*`; env `BACKEND_ORIGIN` (server-only, дефолт `http://localhost:8080`).

- [ ] **Step 1: Добавить rewrites**

```js
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const BACKEND_ORIGIN = process.env.BACKEND_ORIGIN ?? "http://localhost:8080";

/** @type {import('next').NextConfig} */
const nextConfig = {
  outputFileTracingRoot: __dirname,
  async rewrites() {
    return [
      { source: "/api/v1/:path*", destination: `${BACKEND_ORIGIN}/api/v1/:path*` },
      { source: "/static/:path*", destination: `${BACKEND_ORIGIN}/static/:path*` },
    ];
  },
};

export default nextConfig;
```

`/static/*` нужен потому, что `display_fields.go` отдаёт `cover_image: "/static/placeholder-cover.svg"` — относительный путь, который иначе уйдёт в Next и вернёт 404.

- [ ] **Step 2: Записать пример env**

```
# frontend/.env.local.example
NEXT_PUBLIC_USE_MOCK=false
BACKEND_ORIGIN=http://localhost:8080
```

- [ ] **Step 3: Коммит**

```bash
git add frontend/next.config.mjs frontend/.env.local.example
git commit -m "feat: проксировать /api/v1 и /static на Go-шлюз через rewrites"
```

---

### Task 2: Клиент авторизации

**Files:**
- Create: `frontend/lib/api/auth.ts`
- Test: `frontend/lib/api/auth.test.ts`

**Interfaces:**
- Consumes: `API_BASE` из `lib/api/config.ts`.
- Produces: `interface User { id: string; email: string; name: string }`;
  `register(email, password, name): Promise<User>`; `login(email, password): Promise<User>`;
  `logout(): Promise<void>`; `me(): Promise<User | null>` (`null` при 401).

- [ ] **Step 1: Написать падающий тест**

```ts
import { describe, it, expect, vi, afterEach } from "vitest";
import { me, login } from "./auth";

afterEach(() => vi.unstubAllGlobals());

describe("auth", () => {
  it("me() возвращает null на 401, а не бросает", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 401 })));
    await expect(me()).resolves.toBeNull();
  });

  it("login() шлёт credentials и возвращает пользователя", async () => {
    const f = vi.fn(async () =>
      Response.json({ id: "u1", email: "a@b.c", name: "Аня" }));
    vi.stubGlobal("fetch", f);
    await expect(login("a@b.c", "pw")).resolves.toEqual({
      id: "u1", email: "a@b.c", name: "Аня",
    });
    expect(f.mock.calls[0][1]).toMatchObject({ credentials: "include" });
  });

  it("login() бросает с сообщением бэка при 401", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      Response.json({ error: { message: "Неверный email или пароль" } }, { status: 401 })));
    await expect(login("a@b.c", "bad")).rejects.toThrow("Неверный email или пароль");
  });
});
```

- [ ] **Step 2: Прогнать — убедиться, что падает**

Run: `cd frontend && npx vitest run lib/api/auth.test.ts`
Expected: FAIL — `Cannot find module './auth'`

- [ ] **Step 3: Реализовать**

```ts
import { API_BASE } from "./config";

export interface User { id: string; email: string; name: string }

// Go-шлюз заворачивает ошибки в конверт {error:{code,message}}
// (backend/internal/http/middleware/errorenvelope.go). Достаём message,
// чтобы показать пользователю текст бэка, а не "HTTP 401".
async function errorMessage(res: Response, fallback: string): Promise<string> {
  try {
    const body = await res.json();
    return body?.error?.message ?? fallback;
  } catch {
    return fallback;
  }
}

async function post(path: string, body: unknown): Promise<Response> {
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

export async function logout(): Promise<void> {
  await fetch(`${API_BASE}/auth/logout`, { method: "POST", credentials: "include" });
}

export async function me(): Promise<User | null> {
  const res = await fetch(`${API_BASE}/me`, { credentials: "include" });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(await errorMessage(res, "Не удалось проверить сессию"));
  return (await res.json()) as User;
}
```

- [ ] **Step 4: Прогнать — зелёный**

Run: `cd frontend && npx vitest run lib/api/auth.test.ts`
Expected: PASS (3 теста)

- [ ] **Step 5: Коммит**

```bash
git add frontend/lib/api/auth.ts frontend/lib/api/auth.test.ts
git commit -m "feat: клиент авторизации (register/login/logout/me)"
```

---

### Task 3: Экран входа и AuthGate

**Files:**
- Create: `frontend/components/auth/AuthGate.tsx`
- Modify: `frontend/app/page.tsx`
- Test: `frontend/components/auth/AuthGate.test.tsx`

**Interfaces:**
- Consumes: `me`, `login`, `register`, `User` из `lib/api/auth`; `USE_MOCK` из `lib/api/config`.
- Produces: `<AuthGate>{children}</AuthGate>` — при `USE_MOCK` рендерит children сразу
  (мок-режим офлайн, без бэка); иначе проверяет `me()` и показывает форму при `null`.

- [ ] **Step 1: Написать падающий тест**

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AuthGate from "./AuthGate";

vi.mock("@/lib/api/config", () => ({ USE_MOCK: false, API_BASE: "/api/v1" }));
const mocks = vi.hoisted(() => ({ me: vi.fn(), login: vi.fn(), register: vi.fn() }));
vi.mock("@/lib/api/auth", () => mocks);

beforeEach(() => vi.clearAllMocks());

describe("AuthGate", () => {
  it("показывает форму входа, когда сессии нет", async () => {
    mocks.me.mockResolvedValue(null);
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByLabelText("Email")).toBeInTheDocument();
    expect(screen.queryByText("секрет")).not.toBeInTheDocument();
  });

  it("пускает внутрь при живой сессии", async () => {
    mocks.me.mockResolvedValue({ id: "u1", email: "a@b.c", name: "Аня" });
    render(<AuthGate><div>секрет</div></AuthGate>);
    expect(await screen.findByText("секрет")).toBeInTheDocument();
  });

  it("после успешного входа рендерит children", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.login.mockResolvedValue({ id: "u1", email: "a@b.c", name: "Аня" });
    render(<AuthGate><div>секрет</div></AuthGate>);
    await userEvent.type(await screen.findByLabelText("Email"), "a@b.c");
    await userEvent.type(screen.getByLabelText("Пароль"), "pw");
    await userEvent.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByText("секрет")).toBeInTheDocument();
  });

  it("показывает ошибку бэка и не пускает внутрь", async () => {
    mocks.me.mockResolvedValue(null);
    mocks.login.mockRejectedValue(new Error("Неверный email или пароль"));
    render(<AuthGate><div>секрет</div></AuthGate>);
    await userEvent.type(await screen.findByLabelText("Email"), "a@b.c");
    await userEvent.type(screen.getByLabelText("Пароль"), "bad");
    await userEvent.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Неверный email или пароль");
    expect(screen.queryByText("секрет")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Прогнать — убедиться, что падает**

Run: `cd frontend && npx vitest run components/auth/AuthGate.test.tsx`
Expected: FAIL — `Cannot find module './AuthGate'`

- [ ] **Step 3: Реализовать**

```tsx
"use client";
import { useEffect, useState, type FormEvent } from "react";
import { me, login, register, type User } from "@/lib/api/auth";
import { USE_MOCK } from "@/lib/api/config";

type Mode = "login" | "register";

export default function AuthGate({ children }: { children: React.ReactNode }) {
  // Мок-режим не ходит в сеть вообще — приложение должно подниматься офлайн.
  const [user, setUser] = useState<User | null>(null);
  const [checked, setChecked] = useState(USE_MOCK);
  const [mode, setMode] = useState<Mode>("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (USE_MOCK) return;
    let alive = true;
    me()
      .then((u) => { if (alive) setUser(u); })
      .catch(() => { if (alive) setUser(null); })
      .finally(() => { if (alive) setChecked(true); });
    return () => { alive = false; };
  }, []);

  if (USE_MOCK || user) return <>{children}</>;
  if (!checked) {
    return (
      <div className="flex-1 grid place-items-center text-sm text-white/40">
        Проверяем сессию…
      </div>
    );
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const u = mode === "login"
        ? await login(email, password)
        : await register(email, password, name || email);
      setUser(u);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Не удалось войти");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex-1 grid place-items-center px-4">
      <form onSubmit={submit} className="w-full max-w-sm flex flex-col gap-4">
        <h1 className="text-xl text-white/90">
          {mode === "login" ? "Вход" : "Регистрация"}
        </h1>

        {mode === "register" && (
          <label className="flex flex-col gap-1 text-sm text-white/60">
            Имя
            <input
              className="rounded-lg bg-white/5 px-3 py-2 text-white/90 outline-none focus:ring-1 focus:ring-white/20"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoComplete="name"
            />
          </label>
        )}

        <label className="flex flex-col gap-1 text-sm text-white/60">
          Email
          <input
            className="rounded-lg bg-white/5 px-3 py-2 text-white/90 outline-none focus:ring-1 focus:ring-white/20"
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="email"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm text-white/60">
          Пароль
          <input
            className="rounded-lg bg-white/5 px-3 py-2 text-white/90 outline-none focus:ring-1 focus:ring-white/20"
            type="password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={mode === "login" ? "current-password" : "new-password"}
          />
        </label>

        {error && (
          <p role="alert" className="text-sm text-red-400">{error}</p>
        )}

        <button
          type="submit"
          disabled={busy}
          className="rounded-lg bg-white/10 px-3 py-2 text-white/90 disabled:opacity-50"
        >
          {mode === "login" ? "Войти" : "Зарегистрироваться"}
        </button>

        <button
          type="button"
          className="text-sm text-white/40 hover:text-white/70"
          onClick={() => { setMode(mode === "login" ? "register" : "login"); setError(null); }}
        >
          {mode === "login" ? "Нет аккаунта? Регистрация" : "Уже есть аккаунт? Войти"}
        </button>
      </form>
    </div>
  );
}
```

- [ ] **Step 4: Обернуть приложение**

```tsx
import AppShell from "@/components/shell/AppShell";
import AuthGate from "@/components/auth/AuthGate";

export default function Page() {
  return (
    <AuthGate>
      <AppShell />
    </AuthGate>
  );
}
```

- [ ] **Step 5: Прогнать — зелёный**

Run: `cd frontend && npx vitest run components/auth/AuthGate.test.tsx`
Expected: PASS (4 теста)

- [ ] **Step 6: Коммит**

```bash
git add frontend/components/auth frontend/app/page.tsx
git commit -m "feat: экран входа и гейт сессии перед приложением"
```

---

### Task 4: Клиент чатов

**Files:**
- Create: `frontend/lib/api/chats.ts`
- Test: `frontend/lib/api/chats.test.ts`

**Interfaces:**
- Consumes: `API_BASE`.
- Produces: `interface Chat { id: string; title: string; created_at: string }`;
  `createChat(title?): Promise<Chat>`; `listChats(): Promise<Chat[]>`.

- [ ] **Step 1: Написать падающий тест**

```ts
import { describe, it, expect, vi, afterEach } from "vitest";
import { createChat } from "./chats";

afterEach(() => vi.unstubAllGlobals());

describe("createChat", () => {
  it("POST /chats с credentials, возвращает чат", async () => {
    const f = vi.fn(async () =>
      Response.json({ id: "c1", title: "Новый чат", created_at: "2026-07-17T00:00:00Z" },
        { status: 201 }));
    vi.stubGlobal("fetch", f);
    await expect(createChat()).resolves.toMatchObject({ id: "c1" });
    expect(f.mock.calls[0][0]).toBe("/api/v1/chats");
    expect(f.mock.calls[0][1]).toMatchObject({ method: "POST", credentials: "include" });
  });

  it("бросает при ошибке", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 500 })));
    await expect(createChat()).rejects.toThrow();
  });
});
```

- [ ] **Step 2: Прогнать — убедиться, что падает**

Run: `cd frontend && npx vitest run lib/api/chats.test.ts`
Expected: FAIL — `Cannot find module './chats'`

- [ ] **Step 3: Реализовать**

```ts
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

export async function listChats(): Promise<Chat[]> {
  const res = await fetch(`${API_BASE}/chats?limit=20&offset=0`, {
    credentials: "include",
  });
  if (!res.ok) throw new Error(`listChats failed: ${res.status}`);
  const body = await res.json();
  return (body.chats ?? body) as Chat[];
}
```

- [ ] **Step 4: Прогнать — зелёный**

Run: `cd frontend && npx vitest run lib/api/chats.test.ts`
Expected: PASS (2 теста)

- [ ] **Step 5: Коммит**

```bash
git add frontend/lib/api/chats.ts frontend/lib/api/chats.test.ts
git commit -m "feat: клиент чатов (create/list)"
```

---

### Task 5: SSE-клиент поиска

Единственный шов, у которого вообще нет реальной реализации: `ChatScreen.tsx:13` жёстко зашит на `createMockClient()`.

**Files:**
- Create: `frontend/lib/api/searchStream.ts`
- Test: `frontend/lib/api/searchStream.test.ts`

**Interfaces:**
- Consumes: `AgentClient`, `RunHandlers` из `lib/agent/AgentClient`; `AgentEvent`, `Property`, `GeoZone` из `lib/agent/types`; `createChat` из `lib/api/chats`; `API_BASE`, `USE_MOCK`.
- Produces: `createSearchClient(): AgentClient` — реальный, если `USE_MOCK === false`, иначе `createMockClient()`.
  `RunHandlers.onDone` получает `{ properties, zoneGeoJSON, chatId }` — расширение (Task 6 меняет тип).

Go-шлюз шлёт события: `agent_status` `{agent,status,message}`, `text_token` `{token}`, `chat_renamed` `{chat_id,title}`, `final_result` `{suggested_areas_geojson,objects,data_freshness}`, `error` `{code,message}`, `stream_end` `{}`.

- [ ] **Step 1: Написать падающий тест**

```ts
import { describe, it, expect, vi, afterEach } from "vitest";
import { parseSSE } from "./searchStream";

afterEach(() => vi.unstubAllGlobals());

describe("parseSSE", () => {
  it("разбирает кадр в имя события и данные", () => {
    expect(parseSSE('event: text_token\ndata: {"token":"Привет"}'))
      .toEqual({ event: "text_token", data: { token: "Привет" } });
  });

  it("склеивает многострочный data", () => {
    expect(parseSSE('event: error\ndata: {"code":"llm_timeout",\ndata: "message":"Таймаут"}'))
      .toEqual({ event: "error", data: { code: "llm_timeout", message: "Таймаут" } });
  });

  it("возвращает null на битом JSON, а не бросает", () => {
    expect(parseSSE("event: text_token\ndata: {не json")).toBeNull();
  });

  it("stream_end с пустым data разбирается", () => {
    expect(parseSSE("event: stream_end\ndata: {}"))
      .toEqual({ event: "stream_end", data: {} });
  });
});
```

- [ ] **Step 2: Прогнать — убедиться, что падает**

Run: `cd frontend && npx vitest run lib/api/searchStream.test.ts`
Expected: FAIL — `Cannot find module './searchStream'`

- [ ] **Step 3: Реализовать**

```ts
import type { AgentClient, RunHandlers } from "@/lib/agent/AgentClient";
import type { AgentEvent, AgentName, AgentEventStatus, Property, GeoZone } from "@/lib/agent/types";
import { createMockClient } from "@/lib/agent/mockClient";
import { createChat } from "./chats";
import { API_BASE, USE_MOCK } from "./config";

export interface SSEFrame { event: string; data: Record<string, unknown> }

// Разбирает один кадр SSE ("event:" + одна или несколько "data:" строк).
// Битый JSON — не исключение: поток продолжается, кадр молча пропускается.
export function parseSSE(frame: string): SSEFrame | null {
  let event = "message";
  const dataLines: string[] = [];
  for (const raw of frame.split("\n")) {
    const line = raw.trimEnd();
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
  }
  if (!dataLines.length) return null;
  try {
    return { event, data: JSON.parse(dataLines.join("\n")) };
  } catch {
    return null;
  }
}

function createFetchClient(): AgentClient {
  return {
    run(query: string, handlers: RunHandlers) {
      const controller = new AbortController();

      (async () => {
        try {
          const chat = await createChat();
          const res = await fetch(`${API_BASE}/chats/${chat.id}/messages/stream`, {
            method: "POST",
            credentials: "include",
            headers: {
              "Content-Type": "application/json",
              Accept: "text/event-stream",
            },
            body: JSON.stringify({ text: query }),
            signal: controller.signal,
          });

          if (!res.ok || !res.body) {
            handlers.onEvent({
              agent: "orchestrator",
              status: "processing",
              message: `Не удалось начать поток (${res.status})`,
            });
            handlers.onError?.("internal_error", `Не удалось начать поток (${res.status})`);
            return;
          }

          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          let properties: Property[] = [];
          let zoneGeoJSON: GeoZone | null = null;

          const handle = (f: SSEFrame) => {
            if (f.event === "agent_status") {
              handlers.onEvent({
                agent: f.data.agent as AgentName,
                status: f.data.status as AgentEventStatus,
                message: (f.data.message as string) ?? "",
              });
            } else if (f.event === "text_token") {
              handlers.onEvent({
                agent: "orchestrator",
                status: "processing",
                message: "",
                token: (f.data.token as string) ?? "",
              });
            } else if (f.event === "final_result") {
              properties = (f.data.objects as Property[]) ?? [];
              zoneGeoJSON = (f.data.suggested_areas_geojson as GeoZone) ?? null;
            } else if (f.event === "error") {
              handlers.onError?.(
                (f.data.code as string) ?? "internal_error",
                (f.data.message as string) ?? "Ошибка потока",
              );
            }
          };

          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            let sep: number;
            while ((sep = buffer.indexOf("\n\n")) !== -1) {
              const frame = buffer.slice(0, sep);
              buffer = buffer.slice(sep + 2);
              if (!frame.trim()) continue;
              const parsed = parseSSE(frame);
              if (parsed) handle(parsed);
            }
          }

          handlers.onDone({ properties, zoneGeoJSON, chatId: chat.id });
        } catch (err) {
          if (controller.signal.aborted) return; // отмена пользователем — молча
          handlers.onError?.(
            "internal_error",
            err instanceof Error ? err.message : "Сетевая ошибка",
          );
        }
      })();

      return () => controller.abort();
    },
  };
}

export function createSearchClient(): AgentClient {
  return USE_MOCK ? createMockClient() : createFetchClient();
}
```

- [ ] **Step 4: Прогнать — зелёный**

Run: `cd frontend && npx vitest run lib/api/searchStream.test.ts`
Expected: PASS (4 теста)

- [ ] **Step 5: Коммит**

```bash
git add frontend/lib/api/searchStream.ts frontend/lib/api/searchStream.test.ts
git commit -m "feat: SSE-клиент поиска поверх /chats/{id}/messages/stream"
```

---

### Task 6: Расширить шов AgentClient под реальные данные

Сейчас `onDone` принимает только `{properties}`, а `finish()` в сторе подставляет `ZONE_GEOJSON` из мока (`lib/store/session.ts:66`) и нигде не хранит `chatId` — а он нужен паспорту (`getObjectPassport(id, chatId)`) и чату по объекту.

**Files:**
- Modify: `frontend/lib/agent/AgentClient.ts`
- Modify: `frontend/lib/agent/mockClient.ts:39`
- Modify: `frontend/lib/store/session.ts`
- Test: `frontend/lib/store/session.test.ts`

**Interfaces:**
- Consumes: `GeoZone` из `lib/agent/types`; `ZONE_GEOJSON` из `lib/data/mock`.
- Produces: `RunHandlers.onDone(result: { properties: Property[]; zoneGeoJSON?: GeoZone | null; chatId?: string })`;
  `RunHandlers.onError?(code: string, message: string)`;
  `SessionState.chatId: string | null`; `finish(properties, zoneGeoJSON?, chatId?)`.

- [ ] **Step 1: Написать падающий тест**

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { useSession } from "./session";
import type { GeoZone } from "@/lib/agent/types";

const zone: GeoZone = { type: "FeatureCollection", features: [] };

beforeEach(() => useSession.getState().reset());

describe("session.finish", () => {
  it("кладёт зону из аргумента, а не из мока", () => {
    useSession.getState().finish([], zone, "c1");
    expect(useSession.getState().zoneGeoJSON).toBe(zone);
  });

  it("сохраняет chatId", () => {
    useSession.getState().finish([], zone, "c1");
    expect(useSession.getState().chatId).toBe("c1");
  });

  it("падает обратно на мок-зону, когда бэк не прислал", () => {
    useSession.getState().finish([], null, "c1");
    expect(useSession.getState().zoneGeoJSON).not.toBeNull();
  });

  it("reset() чистит chatId", () => {
    useSession.getState().finish([], zone, "c1");
    useSession.getState().reset();
    expect(useSession.getState().chatId).toBeNull();
  });
});
```

- [ ] **Step 2: Прогнать — убедиться, что падает**

Run: `cd frontend && npx vitest run lib/store/session.test.ts`
Expected: FAIL — `finish` принимает один аргумент, `chatId` не существует

- [ ] **Step 3: Расширить AgentClient**

```ts
import type { AgentEvent, Property, GeoZone } from "./types";

export interface RunResult {
  properties: Property[];
  zoneGeoJSON?: GeoZone | null;
  chatId?: string;
}

export interface RunHandlers {
  onEvent(event: AgentEvent): void;
  onDone(result: RunResult): void;
  /** Ошибка потока. Мок его не зовёт — поэтому опционален. */
  onError?(code: string, message: string): void;
}

export interface AgentClient {
  /** Starts a run; returns a cancel function that stops all pending emissions. */
  run(query: string, handlers: RunHandlers): () => void;
}
```

- [ ] **Step 4: Обновить стор**

В `lib/store/session.ts` — добавить `chatId` в интерфейс, в `initial` и переписать `finish`:

```ts
// в interface SessionState:
  chatId: string | null;
  finish: (properties: Property[], zoneGeoJSON?: GeoZone | null, chatId?: string) => void;

// в initial:
  chatId: null as string | null,

// сам finish — зона приходит из final_result; мок-зона остаётся
// запасным вариантом, чтобы карта не пустела в offline-режиме:
  finish: (properties, zoneGeoJSON, chatId) =>
    set({
      properties,
      stage: "done",
      screen: "result",
      zoneGeoJSON: zoneGeoJSON ?? ZONE_GEOJSON,
      chatId: chatId ?? null,
    }),
```

И в `startQuery` пробросить результат целиком плюс обработку ошибки:

```ts
  startQuery: (client, query) => {
    get()._cancel?.();
    set({ stage: "idle", answer: "", screen: "chat", properties: [] });
    const cancel = client.run(query, {
      onEvent: (e) => get().applyEvent(e),
      onDone: (r) => get().finish(r.properties, r.zoneGeoJSON, r.chatId),
      onError: () => set({ stage: "error" }),
    });
    set({ _cancel: cancel });
  },
```

- [ ] **Step 5: Поправить мок под новую сигнатуру**

`lib/agent/mockClient.ts:39` — `handlers.onDone({ properties: PROPERTIES })` остаётся валидным (новые поля опциональны). Менять не нужно; проверить типом.

- [ ] **Step 6: Прогнать — зелёный**

Run: `cd frontend && npx vitest run lib/store/session.test.ts && npx tsc --noEmit`
Expected: PASS (4 теста), tsc без ошибок

- [ ] **Step 7: Коммит**

```bash
git add frontend/lib/agent/AgentClient.ts frontend/lib/store/session.ts frontend/lib/store/session.test.ts
git commit -m "feat: зона и chat_id из final_result вместо мок-константы"
```

---

### Task 7: Включить реальный клиент в ChatScreen и прокинуть chatId

**Files:**
- Modify: `frontend/components/chat/ChatScreen.tsx:8,13`
- Modify: `frontend/components/passport/PassportScreen.tsx:40`
- Test: `frontend/components/chat/ChatScreen.test.tsx`

**Interfaces:**
- Consumes: `createSearchClient` из `lib/api/searchStream`; `chatId` из `lib/store/session`.

- [ ] **Step 1: Заменить мок на шов**

В `ChatScreen.tsx` заменить строку 8 и 13:

```tsx
import { createSearchClient } from "@/lib/api/searchStream";
// ...
  const client = useMemo(() => createSearchClient(), []);
```

- [ ] **Step 2: Прокинуть chatId в паспорт**

В `PassportScreen.tsx` взять `chatId` из стора и передать в `getObjectPassport`:

```tsx
  const chatId = useSession((s) => s.chatId);
  // ...
    getObjectPassport(property.id, chatId ?? undefined)
```

- [ ] **Step 3: Прогнать все тесты фронта**

Run: `cd frontend && npm test && npx tsc --noEmit && npm run lint`
Expected: PASS — все существующие тесты зелёные (мок-режим по умолчанию не меняется, `USE_MOCK` не выставлен)

- [ ] **Step 4: Коммит**

```bash
git add frontend/components/chat/ChatScreen.tsx frontend/components/passport/PassportScreen.tsx
git commit -m "feat: включить реальный SSE-клиент поиска в чате"
```

---

### Task 8: Фронт в docker-compose

Чтобы `docker compose up` поднимал всё, включая UI.

**Files:**
- Create: `frontend/Dockerfile`
- Create: `frontend/.dockerignore`
- Modify: `docker-compose.yml`
- Modify: `frontend/next.config.mjs`

**Interfaces:**
- Consumes: `BACKEND_ORIGIN` (Task 1).
- Produces: сервис `frontend` на порту 3000, `depends_on: backend (healthy)`.

- [ ] **Step 1: Включить standalone-сборку**

В `frontend/next.config.mjs` добавить в `nextConfig`:

```js
  output: "standalone",
```

- [ ] **Step 2: Dockerfile**

```dockerfile
FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:22-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# Пекётся в бандл на этапе сборки — NEXT_PUBLIC_* иначе не попадёт в клиент.
ARG NEXT_PUBLIC_USE_MOCK=false
ENV NEXT_PUBLIC_USE_MOCK=$NEXT_PUBLIC_USE_MOCK
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
COPY --from=builder /app/public ./public
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
EXPOSE 3000
CMD ["node", "server.js"]
```

- [ ] **Step 3: .dockerignore**

```
node_modules
.next
.env.local
```

- [ ] **Step 4: Добавить сервис в compose**

```yaml
  frontend:
    build:
      context: ./frontend
      args:
        NEXT_PUBLIC_USE_MOCK: "false"
    environment:
      # Внутри сети compose ходим по имени сервиса, не по localhost.
      BACKEND_ORIGIN: http://backend:8080
    ports:
      - "3000:3000"
    depends_on:
      backend:
        condition: service_healthy
```

- [ ] **Step 5: Проверить сборку образа**

Run: `docker compose build frontend`
Expected: образ собирается без ошибок

- [ ] **Step 6: Коммит**

```bash
git add frontend/Dockerfile frontend/.dockerignore frontend/next.config.mjs docker-compose.yml
git commit -m "feat: фронт в docker-compose, standalone-сборка"
```

---

### Task 9: Поднять стек и проверить сквозь

**Files:**
- Create: `.env` (не коммитится)

- [ ] **Step 1: Создать .env из шаблона**

```bash
cp .env.example .env
```

Ключи (`OPENROUTER_API_KEY`, `ORS_API_KEY`, `KAGGLE_USERNAME`, `KAGGLE_KEY`) приходят от владельца проекта. Без `OPENROUTER_API_KEY` пайплайн честно деградирует: `degraded: ["nlu"]`, запрос уходит целиком в семантику, объяснение не генерится. Без `ORS_API_KEY` не строятся маршруты в `family_routing`.

- [ ] **Step 2: Поднять стек**

Run: `docker compose up -d --build`
Expected: `db` healthy → `ml-model-cache` completed → `ml-service` healthy → `backend` healthy → `frontend` up.
Первый прогон долгий: `ml-model-cache` тянет `bge-m3` (~2.3 ГБ) и реранкер.

- [ ] **Step 3: Проверить здоровье слоёв**

```bash
curl -s localhost:8080/health
curl -s localhost:8000/health
```
Expected: `{"status":"ok"}` от обоих

- [ ] **Step 4: Проверить, что схема БД поднялась**

```bash
docker compose exec db psql -U habitus -d habitus -c '\dt'
```
Expected: таблицы `users`, `sessions`, `chats`, `messages`, `chat_searches`, `chat_search_results`, `dossier_cache`, `listings`

- [ ] **Step 5: Проверить авторизацию сквозь**

```bash
curl -s -c /tmp/c.txt -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@habitus.local","password":"demo1234","name":"Демо"}'
curl -s -b /tmp/c.txt localhost:8080/api/v1/me
```
Expected: 201 с `{id,email,name}`, затем тот же пользователь из `/me`

- [ ] **Step 6: Залить данные**

```bash
docker compose run --rm ml-service uv run habitus offline --csv /app/data/<файл>.csv
```
Проверить, что объекты есть:
```bash
docker compose exec db psql -U habitus -d habitus -c 'select count(*) from listings'
```
Expected: > 0. Если 0 — поиск вернёт пустой список, и это не баг фронта.

- [ ] **Step 7: Проверить поиск сквозь**

```bash
CHAT=$(curl -s -b /tmp/c.txt -X POST localhost:8080/api/v1/chats \
  -H 'Content-Type: application/json' -d '{}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sN -b /tmp/c.txt -X POST localhost:8080/api/v1/chats/$CHAT/messages/stream \
  -H 'Content-Type: application/json' \
  -d '{"text":"двушка рядом с парком до 15 млн"}'
```
Expected: поток кадров `agent_status` → `text_token` → `final_result` → `stream_end`

- [ ] **Step 8: Проверить в браузере**

Открыть `http://localhost:3000`, зарегистрироваться, отправить запрос.
Expected: стадии агентов идут, ответ стримится по токенам, список результатов — реальные объекты из БД, паспорт открывается.

---

## Self-Review

**Покрытие:** авторизация (2, 3), SSE-поиск (5, 6, 7), чаты (4), прокси и куки (1), запуск одной командой (8), сквозная проверка (9). Паспорт и чат-по-объекту уже имели реальные реализации — им нужен был только `chatId` (задача 7) и `USE_MOCK=false`.

**Известные пробелы, сознательно вне плана:**
- `GET /geo/layers` на фронте не подключён: `LayerToggles` крутит локальные флаги в сторе, реального фетча слоёв нет. Бэк готов (`geo_handler.go`). Это отдельная задача.
- `HistorySidebar` не ходит в `GET /chats` — история остаётся мок-константой.
- `CitySwitch` (`spb`/`msk`) ни на что не влияет: бэк настроен на один регион через `CITY_REGION_CODE`.
