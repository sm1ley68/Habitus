"use client";
import { Field, Input, Select } from "@/components/ui";
import type { OwnerListingDraft } from "@/lib/agent/owner";

const ORIENTATIONS = ["север", "юг", "восток", "запад"];

/** Пустая строка означает «не указано» и уходит как null: ноль здесь был бы
 *  выдуманным замером, а такие правила проекта запрещают. */
function toNumber(value: string): number | null {
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
}

function text(value: number | null | undefined): string {
  return value === null || value === undefined ? "" : String(value);
}

export default function ParamsStep({
  draft, onChange,
}: { draft: OwnerListingDraft; onChange: (patch: OwnerListingDraft) => void }) {
  const orientation = draft.window_orientation ?? [];

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-[15px] tracking-tight text-[#1c1d20]">Что за квартира</h2>
        <p className="mt-1 text-sm text-zinc-500">
          Заполните то, что знаете точно. Пустое поле останется пустым — выдумывать
          за вас мы не будем.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Комнат">
          <Input
            type="number"
            min={0}
            inputMode="numeric"
            value={text(draft.rooms)}
            onChange={(e) => onChange({ rooms: toNumber(e.target.value) })}
          />
        </Field>
        <Field label="Площадь, м²">
          <Input
            inputMode="decimal"
            value={text(draft.area)}
            onChange={(e) => onChange({ area: toNumber(e.target.value) })}
          />
        </Field>
        <Field label="Площадь кухни, м²">
          <Input
            inputMode="decimal"
            value={text(draft.kitchen_area)}
            onChange={(e) => onChange({ kitchen_area: toNumber(e.target.value) })}
          />
        </Field>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Этаж">
            <Input
              type="number"
              min={0}
              inputMode="numeric"
              value={text(draft.level)}
              onChange={(e) => onChange({ level: toNumber(e.target.value) })}
            />
          </Field>
          <Field label="Этажей в доме">
            <Input
              type="number"
              min={0}
              inputMode="numeric"
              value={text(draft.levels)}
              onChange={(e) => onChange({ levels: toNumber(e.target.value) })}
            />
          </Field>
        </div>
      </div>

      <Field label="Окна выходят на" hint="Влияет на то, как мы описываем свет в квартире">
        <Select
          value={orientation[0] ?? ""}
          onChange={(e) => onChange({ window_orientation: e.target.value ? [e.target.value] : [] })}
        >
          <option value="">Не указано</option>
          {ORIENTATIONS.map((side) => (
            <option key={side} value={side}>
              {side}
            </option>
          ))}
        </Select>
      </Field>
    </div>
  );
}
