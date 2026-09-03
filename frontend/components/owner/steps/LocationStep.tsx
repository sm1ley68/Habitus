"use client";
import { Field, Input } from "@/components/ui";
import { CITY_CLOSED_REASON, CITY_LABEL, cityByCoordinates } from "@/lib/city";
import PinMap from "../PinMap";

export default function LocationStep({
  coordinates, address, onCoordinates, onAddress,
}: {
  coordinates: [number, number] | null;
  address: string;
  onCoordinates: (c: [number, number]) => void;
  onAddress: (value: string) => void;
}) {
  // Город берётся из точки, а не спрашивается. Пока точки нет, города тоже
  // нет — предупреждать не о чем.
  const city = coordinates ? cityByCoordinates(coordinates) : null;
  const closed = city ? CITY_CLOSED_REASON[city] : null;

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-[15px] tracking-tight text-[#1c1d20]">Где находится квартира</h2>
        <p className="mt-1 text-sm text-zinc-500">
          Поставьте точку на доме — по ней мы посчитаем дорогу до метро, школ и парков.
        </p>
      </div>

      <PinMap
        value={coordinates}
        onPick={onCoordinates}
        city={city ?? "msk"}
      />

      {/* Публикацию не блокируем: объявление принадлежит продавцу. Но молча
          принять его в город, где никто не ищет, — значит продать пустое
          ожидание. */}
      {closed && city && (
        <p className="rounded-lg bg-[#f8f0e0] px-3 py-2 text-xs text-[#b3822f]">
          {CITY_LABEL[city]} пока не участвует в поиске — {closed}. Объявление
          сохранится и опубликуется, но покупатели пока не найдут его подбором.
        </p>
      )}

      <Field label="Адрес" hint="Показывается покупателю в карточке объявления">
        <Input
          value={address}
          onChange={(e) => onAddress(e.target.value)}
          placeholder="Москва, улица Мельникова, 3к1"
          autoComplete="street-address"
        />
      </Field>
    </div>
  );
}
