import Link from "next/link";
import { Card } from "@/components/ui";

/**
 * Тупик всегда даёт выход: если Циан молчит, объявление можно завести руками,
 * не теряя уже введённую ссылку.
 */
export default function CianUnavailable({ message, url }: { message: string; url: string }) {
  const manual = url ? `/lk/new?url=${encodeURIComponent(url)}` : "/lk/new";

  return (
    <Card className="p-5">
      <p className="text-sm text-[#1c1d20]">{message}</p>
      <p className="mt-2 text-sm text-zinc-500">
        Данные с Циана сейчас не забрать. Можно попробовать позже или заполнить карточку
        самому — это займёт пару минут.
      </p>
      <Link
        href={manual}
        className="mt-4 inline-flex text-sm text-accent transition-opacity hover:opacity-80"
      >
        Заполнить вручную
      </Link>
    </Card>
  );
}
