import { afterEach, describe, expect, test, vi } from "vitest";
import { OwnerApiError, previewImport, listOwnerListings, uploadPhotos } from "./owner";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("owner api", () => {
  test("previewImport передаёт ссылку и разбирает вердикт", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ verdict: "claimable", draft: { id: "1", status: "draft" }, similar: [] }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const preview = await previewImport("https://www.cian.ru/sale/flat/1/");

    expect(preview.verdict).toBe("claimable");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toContain("/owner/listings/import/preview");
    expect(init.credentials).toBe("include");
    expect(JSON.parse(init.body)).toEqual({ url: "https://www.cian.ru/sale/flat/1/" });
  });

  test("ошибка приходит с кодом, а не голым статусом", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      jsonResponse({ error: { code: "cian_unavailable", message: "Циан сейчас не отдаёт данные" } }, 503),
    ));

    await expect(previewImport("https://www.cian.ru/sale/flat/1/")).rejects.toMatchObject({
      code: "cian_unavailable",
      message: "Циан сейчас не отдаёт данные",
    });
  });

  test("ошибка без разбираемого тела не роняет клиент", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("upstream down", { status: 502 })));

    const error = await previewImport("https://www.cian.ru/sale/flat/1/").catch((e) => e);

    expect(error).toBeInstanceOf(OwnerApiError);
    expect(error.code).toBe("internal_error");
    expect(error.message.length).toBeGreaterThan(0);
  });

  test("список возвращает массив даже при пустом ответе", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ listings: null })));
    await expect(listOwnerListings()).resolves.toEqual([]);
  });

  test("загрузка фото уходит как multipart без ручного Content-Type", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: "1", photos: ["/static/uploads/1/a.jpg"] }));
    vi.stubGlobal("fetch", fetchMock);
    const file = new File([new Uint8Array([1, 2, 3])], "a.jpg", { type: "image/jpeg" });

    await uploadPhotos("1", [file]);

    const [, init] = fetchMock.mock.calls[0];
    expect(init.body).toBeInstanceOf(FormData);
    // Границу multipart проставляет браузер; заданный вручную заголовок её ломает.
    expect(init.headers?.["Content-Type"]).toBeUndefined();
  });
});
