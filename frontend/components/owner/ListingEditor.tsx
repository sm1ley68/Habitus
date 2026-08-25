"use client";
import { useState } from "react";
import { Button, Dialog, Field, fieldClass, Input } from "@/components/ui";
import { cityByCoordinates } from "@/lib/city";
import {
  deleteListing, OwnerApiError, publishListing, unpublishListing, updateListing,
} from "@/lib/api/owner";
import type { OwnerListing, OwnerListingDraft } from "@/lib/agent/owner";
import ListingPreview from "./ListingPreview";
import PhotoUploader from "./PhotoUploader";
import PinMap from "./PinMap";
import StatusBadge from "./StatusBadge";

function toNumber(value: string): number | null {
  const trimmed = value.trim().replace(/\s/g, "").replace(",", ".");
  if (trimmed === "") return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
}

function text(value: number | null | undefined): string {
  return value === null || value === undefined ? "" : String(value);
}

// onDeleted вместо useRouter внутри: навигация — забота страницы, а редактор
// остаётся переиспользуемым и тестируемым без роутера.
export default function ListingEditor({
  listing: initial, onDeleted,
}: { listing: OwnerListing; onDeleted?: () => void }) {
  const [listing, setListing] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [confirming, setConfirming] = useState(false);

  const set = (patch: Partial<OwnerListing>) => {
    setListing((current) => ({ ...current, ...patch }));
    setSaved(false);
  };

  const run = async <T,>(action: () => Promise<T>, onDone?: (result: T) => void) => {
    setBusy(true);
    setError(null);
    try {
      onDone?.(await action());
    } catch (e) {
      setError(e instanceof OwnerApiError ? e.message : "Не удалось выполнить действие");
    } finally {
      setBusy(false);
    }
  };

  const save = () =>
    run<OwnerListing>(
      () => {
        const patch: OwnerListingDraft = {
          price: listing.price,
          area: listing.area,
          kitchen_area: listing.kitchen_area,
          rooms: listing.rooms,
          level: listing.level,
          levels: listing.levels,
          address: listing.address,
          description: listing.description,
          window_orientation: listing.window_orientation,
          ...(listing.coordinates
            ? { coordinates: listing.coordinates, city: cityByCoordinates(listing.coordinates) }
            : {}),
        };
        return updateListing(listing.id, patch);
      },
      (updated) => {
        setListing(updated);
        setSaved(true);
      },
    );

  const published = listing.status === "published";
  const pinned = listing.coordinates !== null;

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl tracking-tight text-[#1c1d20]">
            {listing.address || "Объявление без адреса"}
          </h1>
          <p className="mt-1 font-mono text-xs text-zinc-400">{listing.external_id}</p>
        </div>
        <div className="flex flex-col items-end gap-1.5">
          <StatusBadge status={listing.status} />
          {listing.verification === "unverified" && (
            <p className="text-xs text-zinc-400" title="Мы не проверяли, что объявление принадлежит вам">
              Не подтверждено
            </p>
          )}
        </div>
      </header>

      {listing.status === "failed" && listing.import_error && (
        <p role="alert" className="text-sm text-[#b25e4a]">
          {listing.import_error}
        </p>
      )}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
        <div className="flex flex-col gap-3">
          <p className="text-sm text-zinc-500">Так объявление увидит покупатель</p>
          <ListingPreview listing={listing} />
        </div>

        <div className="flex flex-col gap-5">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Цена, ₽">
              <Input
                inputMode="numeric"
                value={text(listing.price)}
                onChange={(e) => set({ price: toNumber(e.target.value) })}
              />
            </Field>
            <Field label="Адрес">
              <Input value={listing.address} onChange={(e) => set({ address: e.target.value })} />
            </Field>
            <Field label="Комнат">
              <Input
                inputMode="numeric"
                value={text(listing.rooms)}
                onChange={(e) => set({ rooms: toNumber(e.target.value) })}
              />
            </Field>
            <Field label="Площадь, м²">
              <Input
                inputMode="decimal"
                value={text(listing.area)}
                onChange={(e) => set({ area: toNumber(e.target.value) })}
              />
            </Field>
            <Field label="Площадь кухни, м²">
              <Input
                inputMode="decimal"
                value={text(listing.kitchen_area)}
                onChange={(e) => set({ kitchen_area: toNumber(e.target.value) })}
              />
            </Field>
            <div className="grid grid-cols-2 gap-4">
              <Field label="Этаж">
                <Input
                  inputMode="numeric"
                  value={text(listing.level)}
                  onChange={(e) => set({ level: toNumber(e.target.value) })}
                />
              </Field>
              <Field label="Этажей">
                <Input
                  inputMode="numeric"
                  value={text(listing.levels)}
                  onChange={(e) => set({ levels: toNumber(e.target.value) })}
                />
              </Field>
            </div>
          </div>

          <Field label="Описание">
            <textarea
              rows={5}
              className={`${fieldClass} resize-y py-2.5`}
              value={listing.description}
              onChange={(e) => set({ description: e.target.value })}
            />
          </Field>

          <div className="flex flex-col gap-2">
            <p className="text-sm text-zinc-500">Точка на карте</p>
            {pinned ? (
              <PinMap
                value={listing.coordinates}
                city={cityByCoordinates(listing.coordinates!)}
                onPick={(c) => set({ coordinates: c })}
              />
            ) : (
              // Ноль вместо отсутствующей точки запрещён: пустая карта честнее
              // метки в Гвинейском заливе.
              <PinMap value={null} onPick={(c) => set({ coordinates: c })} />
            )}
            {!pinned && (
              <p className="text-xs text-[#b25e4a]">
                Поставьте точку на карте — без неё объявление не опубликовать.
              </p>
            )}
          </div>

          <PhotoUploader
            listingId={listing.id}
            photos={listing.photos}
            onChange={(photos) => set({ photos })}
          />
        </div>
      </div>

      {error && (
        <p role="alert" className="text-sm text-[#b25e4a]">
          {error}
        </p>
      )}
      {saved && <p className="text-sm text-[#2f8f5f]">Изменения сохранены</p>}

      <div className="flex flex-wrap gap-2 border-t border-zinc-200 pt-5">
        <Button loading={busy} onClick={() => void save()}>
          Сохранить
        </Button>

        {published ? (
          <Button
            variant="secondary"
            loading={busy}
            onClick={() => void run(() => unpublishListing(listing.id), setListing)}
          >
            Снять с публикации
          </Button>
        ) : (
          <Button
            variant="secondary"
            loading={busy}
            disabled={!pinned}
            title={pinned ? undefined : "Поставьте точку на карте"}
            onClick={() => void run(() => publishListing(listing.id), setListing)}
          >
            {listing.status === "failed" ? "Повторить" : "Опубликовать"}
          </Button>
        )}

        <Button variant="ghost" onClick={() => setConfirming(true)}>
          Удалить
        </Button>
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
                void run(() => deleteListing(listing.id), () => onDeleted?.());
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
    </div>
  );
}
