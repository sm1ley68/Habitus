import { render, screen, act, fireEvent } from "@testing-library/react";
import { test, expect, beforeEach, vi, afterEach } from "vitest";
import LoadMoreButton from "./LoadMoreButton";
import { useSession } from "@/lib/store/session";
import { runResult } from "@/test/fixtures";

beforeEach(() => useSession.getState().reset());
afterEach(() => vi.unstubAllGlobals());

test("кнопки нет, когда тянуть нечего", () => {
  act(() => useSession.getState().finish(runResult({
    properties: [{ id: "A" } as never], total: 1, hasMore: false,
  })));

  render(<LoadMoreButton />);

  expect(screen.queryByRole("button")).not.toBeInTheDocument();
});

test("показывает, сколько показано из скольких", () => {
  act(() => useSession.getState().finish(runResult({
    properties: [{ id: "A" } as never, { id: "B" } as never],
    chatId: "c-1", total: 30, hasMore: true,
  })));

  render(<LoadMoreButton />);

  expect(screen.getByRole("button")).toHaveTextContent("Показать ещё — 2 из 30");
});

test("клик дотягивает остаток", async () => {
  act(() => useSession.getState().finish(runResult({
    properties: [{ id: "A" } as never], chatId: "c-1", total: 2, hasMore: true,
  })));
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
    ok: true, json: async () => ({ objects: [{ id: "B" }], count: 1, total: 2 }),
  }));

  render(<LoadMoreButton />);
  await act(async () => { fireEvent.click(screen.getByRole("button")); });

  expect(useSession.getState().properties.map((p) => p.id)).toEqual(["A", "B"]);
});
