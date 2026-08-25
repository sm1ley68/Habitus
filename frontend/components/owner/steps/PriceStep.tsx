"use client";
import { Field, fieldClass, Input } from "@/components/ui";
import type { OwnerListingDraft } from "@/lib/agent/owner";

function toNumber(value: string): number | null {
  const trimmed = value.trim().replace(/\s/g, "");
  if (trimmed === "") return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
}

export default function PriceStep({
  draft, onChange,
}: { draft: OwnerListingDraft; onChange: (patch: OwnerListingDraft) => void }) {
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-[15px] tracking-tight text-[#1c1d20]">Цена и описание</h2>
        <p className="mt-1 text-sm text-zinc-500">
          Опишите квартиру так, как рассказали бы соседу: чем она хороша и для кого.
        </p>
      </div>

      <Field label="Цена, ₽" hint="Полная стоимость квартиры">
        <Input
          inputMode="numeric"
          value={draft.price === null || draft.price === undefined ? "" : String(draft.price)}
          onChange={(e) => onChange({ price: toNumber(e.target.value) })}
          placeholder="12500000"
        />
      </Field>

      <Field label="Описание">
        <textarea
          rows={6}
          className={`${fieldClass} resize-y py-2.5`}
          value={draft.description ?? ""}
          onChange={(e) => onChange({ description: e.target.value })}
          placeholder="Тихая двушка окнами во двор, до метро десять минут пешком"
        />
      </Field>
    </div>
  );
}
