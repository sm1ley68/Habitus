"use client";
import { useCallback, useEffect, useState } from "react";

// Лента снимков объявления под геро-баннером. Источник — ObjectPassport.images
// (все фото из listings.photos), тогда как геро показывает только обложку.
//
// Две вещи здесь не косметические:
//  * ленивая загрузка миниатюр — на объявление приходится в среднем 22 снимка,
//    максимум 51; без lazy открытие карточки тянуло бы мегабайты разом;
//  * заглушка снимком не считается. Пустая галерея не рендерится вовсе —
//    отсутствие данных деградирует до отсутствия блока, а не до пустой рамки.
const PLACEHOLDER = "/static/placeholder-cover.svg";

export default function PhotoGallery({
  images,
  alt,
}: {
  images: string[];
  alt: string;
}) {
  const photos = (images ?? []).filter((u) => u && u !== PLACEHOLDER);
  const [openAt, setOpenAt] = useState<number | null>(null);

  const close = useCallback(() => setOpenAt(null), []);
  const step = useCallback(
    (delta: number) =>
      setOpenAt((i) =>
        i === null ? i : Math.min(photos.length - 1, Math.max(0, i + delta)),
      ),
    [photos.length],
  );

  useEffect(() => {
    if (openAt === null) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
      if (e.key === "ArrowRight") step(1);
      if (e.key === "ArrowLeft") step(-1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openAt, close, step]);

  if (!photos.length) return null;

  return (
    <section className="mx-auto w-full max-w-5xl px-6 py-6">
      <div className="flex gap-2 overflow-x-auto pb-2 [scrollbar-width:thin]">
        {photos.map((src, i) => (
          <button
            key={src}
            type="button"
            aria-label={`Открыть фото ${i + 1} из ${photos.length}`}
            onClick={() => setOpenAt(i)}
            className="group relative h-28 w-40 shrink-0 overflow-hidden rounded-lg bg-zinc-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={src}
              alt={`${alt} — фото ${i + 1}`}
              loading="lazy"
              className="h-full w-full object-cover transition-transform duration-300 ease-out group-hover:scale-[1.05]"
            />
          </button>
        ))}
      </div>

      {openAt !== null && (
        <div
          role="dialog"
          aria-modal="true"
          aria-label={`Фото ${openAt + 1} из ${photos.length}`}
          onClick={close}
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/85 p-6 backdrop-blur-sm"
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            data-testid="lightbox-image"
            src={photos[openAt]}
            alt={`${alt} — фото ${openAt + 1}`}
            onClick={(e) => e.stopPropagation()}
            className="max-h-full max-w-full rounded-lg object-contain"
          />

          {/* Следующий снимок подгружаем заранее, чтобы листание не моргало. */}
          {photos[openAt + 1] && (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={photos[openAt + 1]} alt="" aria-hidden className="hidden" />
          )}

          <button
            type="button"
            aria-label="Закрыть"
            onClick={close}
            className="absolute right-5 top-5 rounded-full bg-white/10 px-3 py-1.5 text-white/85 transition-colors hover:text-white"
          >
            ✕
          </button>

          <button
            type="button"
            aria-label="Предыдущее фото"
            disabled={openAt === 0}
            onClick={(e) => { e.stopPropagation(); step(-1); }}
            className="absolute left-5 rounded-full bg-white/10 px-4 py-3 text-white/85 transition-colors hover:text-white disabled:opacity-30"
          >
            ←
          </button>

          <button
            type="button"
            aria-label="Следующее фото"
            disabled={openAt === photos.length - 1}
            onClick={(e) => { e.stopPropagation(); step(1); }}
            className="absolute right-5 rounded-full bg-white/10 px-4 py-3 text-white/85 transition-colors hover:text-white disabled:opacity-30"
          >
            →
          </button>

          <span className="absolute bottom-6 font-mono text-sm text-white/70">
            {openAt + 1} / {photos.length}
          </span>
        </div>
      )}
    </section>
  );
}
