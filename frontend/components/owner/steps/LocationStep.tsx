"use client";
import { Field, Input } from "@/components/ui";
import { cityByCoordinates } from "@/lib/city";
import PinMap from "../PinMap";

export default function LocationStep({
  coordinates, address, onCoordinates, onAddress,
}: {
  coordinates: [number, number] | null;
  address: string;
  onCoordinates: (c: [number, number]) => void;
  onAddress: (value: string) => void;
}) {
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
        city={coordinates ? cityByCoordinates(coordinates) : "msk"}
      />

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
