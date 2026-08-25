"use client";
import { useId, useState, type ChangeEvent } from "react";
import { deletePhoto, OwnerApiError, uploadPhotos } from "@/lib/api/owner";

export default function PhotoUploader({
  listingId, photos, onChange,
}: { listingId: string; photos: string[]; onChange: (photos: string[]) => void }) {
  const inputId = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const message = (e: unknown, fallback: string) =>
    e instanceof OwnerApiError ? e.message : fallback;

  const add = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    // Значение сбрасываем сразу: иначе повторный выбор того же файла не даст
    // события change и загрузка молча не произойдёт.
    event.target.value = "";
    if (files.length === 0) return;

    setBusy(true);
    setError(null);
    try {
      const updated = await uploadPhotos(listingId, files);
      onChange(updated.photos);
    } catch (e) {
      setError(message(e, "Не удалось загрузить фотографии"));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (url: string) => {
    setBusy(true);
    setError(null);
    try {
      const updated = await deletePhoto(listingId, url);
      onChange(updated.photos);
    } catch (e) {
      setError(message(e, "Не удалось удалить фотографию"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <div>
        <label
          htmlFor={inputId}
          className="inline-flex min-h-11 cursor-pointer items-center rounded-lg border border-zinc-200 bg-white px-4 text-sm text-[#1c1d20] transition-colors hover:border-zinc-300 hover:bg-zinc-50"
        >
          Добавить фото
        </label>
        <input
          id={inputId}
          type="file"
          multiple
          accept="image/jpeg,image/png,image/webp"
          disabled={busy}
          onChange={(e) => void add(e)}
          className="sr-only"
        />
        <p className="mt-2 text-xs text-zinc-400">JPEG, PNG или WebP</p>
      </div>

      {error && (
        <p role="alert" className="text-sm text-[#b25e4a]">
          {error}
        </p>
      )}

      {photos.length > 0 && (
        <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {photos.map((url, index) => (
            <li key={url} className="relative overflow-hidden rounded-xl bg-zinc-100">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={url} alt={`Фото ${index + 1}`} className="h-28 w-full object-cover" />
              <button
                type="button"
                aria-label={`Удалить фото ${index + 1}`}
                disabled={busy}
                onClick={() => void remove(url)}
                className="absolute right-2 top-2 grid h-8 w-8 place-items-center rounded-full bg-white/90 text-sm text-[#b25e4a] transition-colors hover:bg-white disabled:opacity-50"
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
