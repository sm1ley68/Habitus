import { money } from "@/lib/format";
import type { SimilarListing } from "@/lib/agent/owner";

/**
 * Предупреждение, а не запрет: квартиру часто перевыставляют под новым id,
 * и совпадение по дому, комнатам и площади — повод посмотреть, а не отказать.
 */
export default function SimilarWarning({ similar }: { similar: SimilarListing[] }) {
  if (similar.length === 0) return null;

  return (
    <div
      role="alert"
      className="rounded-xl border border-[#ecdfc4] bg-[#f8f0e0] px-4 py-3 text-sm text-[#8a6524]"
    >
      <p>Похоже, эта квартира уже есть в базе:</p>
      <ul className="mt-2 flex flex-col gap-1">
        {similar.map((item) => (
          <li key={item.external_id} className="font-mono text-xs">
            {item.address || "адрес не указан"} · {money(item.price)}
          </li>
        ))}
      </ul>
      <p className="mt-2 text-xs">
        Если это ваша квартира, продолжайте — дубль в витрине не появится.
      </p>
    </div>
  );
}
