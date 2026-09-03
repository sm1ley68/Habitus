import { describe, it, expect, vi, afterEach } from "vitest";
import { sendLead, LeadError } from "./lead";

afterEach(() => vi.unstubAllGlobals());

describe("sendLead", () => {
  it("отправляет заявку и возвращает признак регистрации", async () => {
    const f = vi.fn(async () => Response.json(
      { lead: { id: "l1" }, registered: true }, { status: 201 }));
    vi.stubGlobal("fetch", f);
    const result = await sendLead("obj-1", { name: "Иван", contact: "+7 999" },
      { email: "i@e.test", password: "password1" });
    expect(result.registered).toBe(true);
    const body = JSON.parse((f.mock.calls[0][1] as RequestInit).body as string);
    expect(body).toMatchObject({
      name: "Иван", contact: "+7 999", register: { email: "i@e.test" },
    });
    // message пустой — в тело не кладём: бэк отличает «не написал» от пустой строки.
    expect(body).not.toHaveProperty("message");
  });

  it("поднимает код registration_required, а не только текст", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Response.json(
      { error: { code: "registration_required", message: "Зарегистрируйтесь" } },
      { status: 403 })));
    await expect(sendLead("obj-1", { name: "И", contact: "c" }))
      .rejects.toMatchObject({ code: "registration_required" });
    await expect(sendLead("obj-1", { name: "И", contact: "c" }))
      .rejects.toBeInstanceOf(LeadError);
  });
});
