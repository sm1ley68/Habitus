"use client";
import Link from "next/link";
import { Button, Card } from "@/components/ui";
import { money } from "@/lib/format";
import type { ImportPreview } from "@/lib/agent/owner";

const HEADING: Record<ImportPreview["verdict"], string> = {
  new: "Нашли объявление",
  claimable: "Мы уже знаем эту квартиру",
  already_yours: "Объявление уже в вашем кабинете",
};

const ACTION: Record<ImportPreview["verdict"], string> = {
  new: "Импортировать",
  claimable: "Это моя квартира",
  already_yours: "",
};

function specs(draft: ImportPreview["draft"]): string {
  const parts: string[] = [];
  if (draft.rooms !== null) parts.push(`${draft.rooms} комн`);
  if (draft.area !== null) parts.push(`${draft.area.toLocaleString("ru-RU")} м²`);
  if (draft.level !== null) {
    parts.push(draft.levels !== null ? `${draft.level}/${draft.levels} этаж` : `${draft.level} этаж`);
  }
  return parts.join(" · ");
}

export default function ImportPreviewCard({
  preview, busy, onConfirm,
}: { preview: ImportPreview; busy: boolean; onConfirm: () => void }) {
  const { draft, verdict } = preview;

  return (
    <Card className="p-5">
      <p className="text-[15px] tracking-tight text-[#1c1d20]">{HEADING[verdict]}</p>
      {verdict === "claimable" && (
        <p className="mt-1 text-sm text-zinc-500">
          Она уже есть в нашей базе — заберите её себе, и карточка станет вашей.
        </p>
      )}

      <div className="mt-4 border-t border-zinc-100 pt-4">
        <p className="text-sm text-[#1c1d20]">{draft.address || "Адрес не указан"}</p>
        <p className="mt-1.5 font-mono text-sm text-zinc-700">{money(draft.price)}</p>
        {specs(draft) && <p className="mt-0.5 font-mono text-xs text-zinc-400">{specs(draft)}</p>}
      </div>

      <div className="mt-5">
        {verdict === "already_yours" && preview.existing_id ? (
          <Link href={`/lk/listings/${preview.existing_id}`}>
            <Button variant="secondary">Открыть</Button>
          </Link>
        ) : (
          <Button loading={busy} onClick={onConfirm}>
            {ACTION[verdict]}
          </Button>
        )}
      </div>
    </Card>
  );
}
