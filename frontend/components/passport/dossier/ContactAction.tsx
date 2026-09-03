"use client";
import { useState, type FormEvent } from "react";
import { LeadError, sendLead } from "@/lib/api/lead";
import { useAuth } from "@/lib/store/auth";
import type { PassportContact } from "@/lib/agent/types";

const FIELD =
  "min-h-11 w-full rounded-lg border border-zinc-200 bg-white px-3 py-2 text-sm text-[#1c1d20] " +
  "outline-none transition-colors placeholder:text-zinc-400 focus:border-accent";

/**
 * Единственное действие паспорта. Что именно показать, решает бэк полем
 * contact.kind, а не фронт по косвенным признакам:
 *   lead     — объявление ведёт продавец в кабинете → форма заявки;
 *   external — витринный объект → уход на источник по source_url;
 *   none     — связаться нечем, действия нет. Выдуманная кнопка ведёт в
 *              никуда и хуже её отсутствия.
 *
 * Поля contact у старых ответов шлюза может не быть — тогда тоже ничего не
 * рисуем: гадать по source_url значит подменять контракт догадкой.
 */
export default function ContactAction({
  objectId, contact,
}: { objectId: string; contact?: PassportContact }) {
  if (!contact || contact.kind === "none") return null;

  if (contact.kind === "external") {
    return (
      <Section title="Объявление на источнике">
        <p className="max-w-[52ch] text-sm leading-relaxed text-zinc-500">
          Этот объект есть в витрине Habitus, но продавца в системе нет —
          связаться можно только на площадке, откуда пришло объявление.
        </p>
        {contact.source_url && (
          <a
            href={contact.source_url}
            target="_blank"
            rel="noopener noreferrer"
            className="mt-5 inline-flex min-h-11 items-center rounded-lg border border-zinc-200 bg-white px-4 text-sm text-[#1c1d20] transition-colors hover:border-zinc-300 hover:bg-zinc-50"
          >
            Открыть на источнике ↗
          </a>
        )}
      </Section>
    );
  }

  return <LeadForm objectId={objectId} />;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-zinc-100 bg-white">
      <div className="mx-auto w-full max-w-5xl px-6 py-10">
        <h2 className="text-lg tracking-tight text-[#1c1d20]">{title}</h2>
        <div className="mt-3">{children}</div>
      </div>
    </section>
  );
}

/**
 * Гостю здесь не отказывают: аккаунт заводится тем же запросом, что и заявка —
 * отдельный поход на регистрацию потерял бы заполненную форму, а вместе с ней
 * и заявку. Раз из /me уже известно, что перед нами гость, поля email/пароля
 * показываются сразу; 403 registration_required остаётся страховкой на случай,
 * когда сессия успела смениться между загрузкой паспорта и отправкой.
 */
function LeadForm({ objectId }: { objectId: string }) {
  const user = useAuth((s) => s.user);
  const setUser = useAuth((s) => s.setUser);
  const isGuest = user?.is_guest ?? false;

  const [name, setName] = useState("");
  const [contact, setContact] = useState("");
  const [message, setMessage] = useState("");
  const [needsAccount, setNeedsAccount] = useState(isGuest);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const registration =
        needsAccount && email && password ? { email, password } : undefined;
      const result = await sendLead(objectId, { name, contact, message }, registration);
      if (result.registered && user) {
        // Апгрейд гостя не меняет id пользователя — обновляем только признак,
        // чтобы призывы зарегистрироваться исчезли по всему приложению.
        setUser({ ...user, is_guest: false, email });
      }
      setSent(true);
    } catch (err) {
      if (err instanceof LeadError && err.code === "registration_required") {
        // Это приглашение, а не отказ: раскрываем поля, ничего не очищая.
        setNeedsAccount(true);
        setError(err.message);
      } else {
        setError(err instanceof Error ? err.message : "Не удалось отправить заявку");
      }
    } finally {
      setBusy(false);
    }
  };

  if (sent) {
    return (
      <Section title="Написать продавцу">
        <p className="text-sm text-[#2f8f5f]">
          Заявка отправлена — продавец увидит её в своём кабинете.
        </p>
      </Section>
    );
  }

  return (
    <Section title="Написать продавцу">
      <form onSubmit={submit} className="grid max-w-xl gap-3">
        <input value={name} onChange={(e) => setName(e.target.value)}
          placeholder="Как вас зовут" aria-label="Имя" required className={FIELD} />
        <input value={contact} onChange={(e) => setContact(e.target.value)}
          placeholder="Телефон или почта для ответа" aria-label="Контакт" required
          className={FIELD} />
        <textarea value={message} onChange={(e) => setMessage(e.target.value)}
          placeholder="Сообщение продавцу (необязательно)" aria-label="Сообщение"
          rows={3} className={`${FIELD} min-h-20 resize-y py-2`} />

        {needsAccount && (
          <>
            <p className="text-xs leading-5 text-zinc-500">
              Заявка приходит от аккаунта — заведём его этим же отправлением.
              Всё, что вы уже нашли и сохранили, останется при вас.
            </p>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              placeholder="Email" aria-label="Email" autoComplete="email" required
              className={FIELD} />
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              placeholder="Пароль" aria-label="Пароль" autoComplete="new-password" required
              className={FIELD} />
          </>
        )}

        {error && <p role="alert" className="text-sm text-[#b25e4a]">{error}</p>}

        <button type="submit" disabled={busy}
          className="min-h-11 justify-self-start rounded-lg bg-[#1c1d20] px-4 text-sm text-white transition-opacity hover:opacity-90 disabled:opacity-50">
          {busy ? "…" : "Отправить заявку"}
        </button>
      </form>
    </Section>
  );
}
