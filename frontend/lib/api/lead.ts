import type { Lead } from "@/lib/agent/types";
import { API_BASE } from "./config";

export interface LeadInput {
  name: string;
  contact: string;
  message?: string;
}

/** Регистрация прямо в форме заявки — гостю не отказывают, аккаунт заводится
 *  тем же запросом. Отдельный поход на регистрацию потерял бы заполненную
 *  форму, а вместе с ней и заявку. */
export interface LeadRegistration {
  email: string;
  password: string;
}

export interface LeadResult {
  lead: Lead;
  /** true — сессия сменилась, гость стал аккаунтом. id пользователя не менялся. */
  registered: boolean;
}

/** Код отказа из конверта шлюза. Хендлер формы различает их по коду, а не по
 *  тексту: registration_required — это приглашение раскрыть email/пароль,
 *  а не ошибка, и показывать его как ошибку было бы враньём. */
export class LeadError extends Error {
  constructor(readonly code: string, message: string) {
    super(message);
    this.name = "LeadError";
  }
}

export async function sendLead(
  objectId: string, input: LeadInput, register?: LeadRegistration,
): Promise<LeadResult> {
  const res = await fetch(`${API_BASE}/objects/${encodeURIComponent(objectId)}/lead`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: input.name,
      contact: input.contact,
      ...(input.message ? { message: input.message } : {}),
      ...(register ? { register } : {}),
    }),
  });
  if (!res.ok) {
    let code = "internal_error";
    let message = "Не удалось отправить заявку";
    try {
      const body = await res.json();
      code = body?.error?.code ?? code;
      message = body?.error?.message ?? message;
    } catch {
      // Тело не разобралось — остаются дефолты выше. Выдумывать код нельзя:
      // по нему форма решает, раскрывать ли поля регистрации.
    }
    throw new LeadError(code, message);
  }
  const body = await res.json();
  return { lead: body.lead as Lead, registered: Boolean(body.registered) };
}
