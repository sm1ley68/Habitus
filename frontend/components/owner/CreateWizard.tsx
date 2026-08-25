"use client";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Button, Card } from "@/components/ui";
import { cityByCoordinates } from "@/lib/city";
import { createListing, OwnerApiError, publishListing, updateListing } from "@/lib/api/owner";
import type { OwnerListingDraft } from "@/lib/agent/owner";
import LocationStep from "./steps/LocationStep";
import ParamsStep from "./steps/ParamsStep";
import PhotosStep from "./steps/PhotosStep";
import PriceStep from "./steps/PriceStep";

const STEPS = 4;

/**
 * Мастер создания объявления.
 *
 * Черновик уходит на бэк сразу после первого шага и правится на каждом
 * следующем: закрытая вкладка не должна стоить продавцу заполненной формы.
 * Город не спрашиваем — он выводится из поставленной точки.
 */
export default function CreateWizard({ hint }: { hint?: string }) {
  const router = useRouter();
  const [step, setStep] = useState(1);
  const [listingId, setListingId] = useState<string | null>(null);
  const [coordinates, setCoordinates] = useState<[number, number] | null>(null);
  const [address, setAddress] = useState("");
  const [photos, setPhotos] = useState<string[]>([]);
  const [draft, setDraft] = useState<OwnerListingDraft>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const patch = (part: OwnerListingDraft) => setDraft((current) => ({ ...current, ...part }));

  const payload = (): OwnerListingDraft => ({
    ...draft,
    address,
    ...(coordinates ? { coordinates, city: cityByCoordinates(coordinates) } : {}),
  });

  const save = async (): Promise<boolean> => {
    setBusy(true);
    setError(null);
    try {
      if (listingId === null) {
        const created = await createListing(payload());
        setListingId(created.id);
        setPhotos(created.photos ?? []);
      } else {
        const updated = await updateListing(listingId, payload());
        setPhotos(updated.photos ?? []);
      }
      return true;
    } catch (e) {
      setError(e instanceof OwnerApiError ? e.message : "Не удалось сохранить черновик");
      return false;
    } finally {
      setBusy(false);
    }
  };

  const next = async () => {
    if (step === 1 && coordinates === null) {
      setError("Поставьте точку на карте — без неё объявление не опубликовать");
      return;
    }
    if (await save()) setStep((s) => Math.min(s + 1, STEPS));
  };

  const publish = async () => {
    if (!(await save())) return;
    if (listingId === null) return;
    setBusy(true);
    try {
      const published = await publishListing(listingId);
      router.push(`/lk/listings/${published.id}`);
    } catch (e) {
      setError(e instanceof OwnerApiError ? e.message : "Не удалось опубликовать объявление");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <div>
        <h1 className="text-xl tracking-tight text-[#1c1d20]">Новое объявление</h1>
        <p className="mt-2 font-mono text-xs text-zinc-400">
          Шаг {step} из {STEPS}
        </p>
        {hint && <p className="mt-2 text-sm text-zinc-500">{hint}</p>}
      </div>

      <Card className="p-6">
        {step === 1 && (
          <LocationStep
            coordinates={coordinates}
            address={address}
            onCoordinates={(c) => {
              setCoordinates(c);
              setError(null);
            }}
            onAddress={setAddress}
          />
        )}
        {step === 2 && <ParamsStep draft={draft} onChange={patch} />}
        {step === 3 && <PhotosStep listingId={listingId} photos={photos} onChange={setPhotos} />}
        {step === 4 && <PriceStep draft={draft} onChange={patch} />}
      </Card>

      {error && (
        <p role="alert" className="text-sm text-[#b25e4a]">
          {error}
        </p>
      )}

      <div className="flex items-center justify-between gap-3">
        <Button
          variant="ghost"
          disabled={step === 1 || busy}
          onClick={() => setStep((s) => Math.max(s - 1, 1))}
        >
          Назад
        </Button>

        {step < STEPS ? (
          <Button loading={busy} onClick={() => void next()}>
            Далее
          </Button>
        ) : (
          <Button loading={busy} onClick={() => void publish()}>
            Опубликовать
          </Button>
        )}
      </div>
    </div>
  );
}
