"use client";
import { useEffect } from "react";
import { money } from "@/lib/format";
import { useEngagement } from "@/lib/store/engagement";
import { useSession } from "@/lib/store/session";
import SaveButton from "./SaveButton";

/**
 * Сохранённые объекты. Shortlist переживает сессию и апгрейд гостя в аккаунт —
 * без экрана, где его видно, сохранение было бы жестом в пустоту.
 *
 * Процента совпадения тут намеренно нет: он посчитан под конкретный запрос, а
 * не про объект, и показывать его рядом с карточкой из другого подбора значит
 * выдавать чужую цифру за оценку этого объекта.
 */
export default function SavedScreen() {
  const favorites = useEngagement((s) => s.favorites);
  const hydrated = useEngagement((s) => s.hydrated);
  const hydrate = useEngagement((s) => s.hydrate);
  const openFromMap = useSession((s) => s.openListingFromMap);

  useEffect(() => { void hydrate(); }, [hydrate]);

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-5xl px-6 py-10">
        <h1 className="text-xl tracking-tight text-[#1c1d20]">Сохранённое</h1>
        <p className="mt-1.5 text-sm text-zinc-500">
          {hydrated
            ? `${favorites.length} ${plural(favorites.length)}`
            : "Загружаем…"}
        </p>

        {hydrated && favorites.length === 0 && (
          <div className="mt-8 grid min-h-48 place-items-center rounded-2xl border border-dashed border-zinc-200 px-6 text-center">
            <div>
              <p className="text-sm font-medium text-zinc-700">Пока ничего не сохранено</p>
              <p className="mt-1.5 text-xs leading-5 text-zinc-400">
                Сердечко на карточке выдачи оставит объект здесь — он переживёт
                и сессию, и регистрацию.
              </p>
            </div>
          </div>
        )}

        <div className="mt-6 grid gap-3 sm:grid-cols-2">
          {favorites.map((f) => (
            <div
              key={f.id}
              className="relative overflow-hidden rounded-2xl border border-zinc-200 bg-white"
            >
              <button
                type="button"
                aria-label={`Открыть ${f.address || f.name}`}
                onClick={() => openFromMap({
                  id: f.id, name: f.name, address: f.address,
                  cover_image: f.cover_image, match_score: null,
                  price_from: f.price_from, rooms: f.rooms, area_sqm: f.area_sqm,
                  floor: f.floor, tags: [], coordinates: f.coordinates,
                })}
                className="absolute inset-0 z-0 cursor-pointer rounded-2xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              />
              <div className="pointer-events-none relative aspect-[3/2] bg-zinc-100">
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={f.cover_image} alt={f.name} loading="lazy"
                  className="absolute inset-0 h-full w-full object-cover" />
                <div className="pointer-events-auto absolute left-3 top-3 z-10">
                  <SaveButton objectId={f.id} label={f.address || f.name} />
                </div>
              </div>
              <div className="pointer-events-none relative z-10 p-4">
                <h2 className="text-sm font-medium tracking-tight text-[#1c1d20]">
                  {f.address || f.name}
                </h2>
                <p className="mt-1 font-mono text-sm text-zinc-700">{money(f.price_from)}</p>
                <p className="mt-0.5 text-xs text-zinc-400">
                  {f.rooms}-комн · {f.area_sqm} м² · {f.floor} этаж
                </p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function plural(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return "объект";
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return "объекта";
  return "объектов";
}
