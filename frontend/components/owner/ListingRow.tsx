"use client";
import Link from "next/link";
import { useState } from "react";
import { Button, Card, Dialog } from "@/components/ui";
import { deleteListing, publishListing, unpublishListing } from "@/lib/api/owner";
import { OwnerApiError } from "@/lib/api/owner";
import { money } from "@/lib/format";
import type { OwnerListing } from "@/lib/agent/owner";
import StatusBadge, { STATUS_RAIL } from "./StatusBadge";

/** Строка характеристик собирается только из заполненных полей: пропуск честнее
 *  прочерка на месте того, чего продавец не указывал. */
function specs(listing: OwnerListing): string {
  const parts: string[] = [];
  if (listing.rooms !== null) parts.push(`${listing.rooms} комн`);
  if (listing.area !== null) parts.push(`${listing.area.toLocaleString("ru-RU")} м²`);
  if (listing.level !== null) {
    parts.push(listing.levels !== null ? `${listing.level}/${listing.levels} этаж` : `${listing.level} этаж`);
  }
  return parts.join(" · ");
}

function updatedAt(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString("ru-RU", { day: "numeric", month: "long" });
}

export default function ListingRow({
  listing, onChanged,
}: { listing: OwnerListing; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (action: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      onChanged();
    } catch (e) {
      setError(e instanceof OwnerApiError ? e.message : "Не удалось выполнить действие");
    } finally {
      setBusy(false);
    }
  };

  const cover = listing.photos[0];
  const published = listing.status === "published";

  return (
    <Card className="relative overflow-hidden">
      {/* Статусный кант на торце карточки: состояние объявления — первое, что
          нужно продавцу, и оно читается ещё до текста. */}
      <span
        aria-hidden
        style={{ backgroundColor: STATUS_RAIL[listing.status] }}
        className="absolute inset-y-0 left-0 w-1"
      />

      <div className="flex flex-col gap-4 p-5 pl-6 sm:flex-row">
        <div className="h-24 w-full shrink-0 overflow-hidden rounded-xl bg-zinc-100 sm:w-32">
          {cover && (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={cover} alt="" loading="lazy" className="h-full w-full object-cover" />
          )}
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <Link
              href={`/lk/listings/${listing.id}`}
              className="font-medium text-[15px] tracking-tight text-[#1c1d20] hover:text-accent"
            >
              {listing.address || "Без адреса"}
            </Link>
            <StatusBadge status={listing.status} />
          </div>

          <p className="mt-1.5 font-mono text-sm text-zinc-700">{money(listing.price)}</p>
          {specs(listing) && <p className="mt-0.5 font-mono text-xs text-zinc-400">{specs(listing)}</p>}
          <p className="mt-2 text-xs text-zinc-400">Обновлено {updatedAt(listing.updated_at)}</p>

          {listing.status === "failed" && listing.import_error && (
            <p className="mt-2 text-xs text-[#b25e4a]">{listing.import_error}</p>
          )}
          {error && (
            <p role="alert" className="mt-2 text-xs text-[#b25e4a]">
              {error}
            </p>
          )}

          <div className="mt-4 flex flex-wrap gap-2">
            {listing.status === "failed" && (
              <Button variant="secondary" loading={busy} onClick={() => run(() => publishListing(listing.id))}>
                Повторить
              </Button>
            )}
            {published && (
              <Button variant="secondary" loading={busy} onClick={() => run(() => unpublishListing(listing.id))}>
                Снять с публикации
              </Button>
            )}
            {!published && listing.status !== "failed" && (
              <Button loading={busy} onClick={() => run(() => publishListing(listing.id))}>
                Опубликовать
              </Button>
            )}
            <Button variant="ghost" onClick={() => setConfirming(true)}>
              Удалить
            </Button>
          </div>
        </div>
      </div>

      <Dialog
        open={confirming}
        title="Удалить объявление?"
        onClose={() => setConfirming(false)}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirming(false)}>
              Отмена
            </Button>
            <Button
              variant="danger"
              loading={busy}
              onClick={() => {
                setConfirming(false);
                void run(() => deleteListing(listing.id));
              }}
            >
              Удалить
            </Button>
          </>
        }
      >
        Объявление исчезнет из поиска и из кабинета. Восстановить его не получится —
        придётся заводить заново.
      </Dialog>
    </Card>
  );
}
