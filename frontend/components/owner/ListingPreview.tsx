import { money } from "@/lib/format";
import type { OwnerListing } from "@/lib/agent/owner";

/**
 * Объявление в том виде, в каком его увидит покупатель в выдаче. Оформление
 * повторяет PropertyCard, но компонент отдельный: у карточки покупателя другой
 * источник данных (Property), и связывать их значило бы тянуть чужую форму.
 */
export default function ListingPreview({ listing }: { listing: OwnerListing }) {
  const specs = [
    listing.rooms !== null ? `${listing.rooms}-комн` : null,
    listing.area !== null ? `${listing.area.toLocaleString("ru-RU")} м²` : null,
    listing.level !== null ? `${listing.level} этаж` : null,
  ].filter(Boolean).join(" · ");

  const cover = listing.photos[0];

  return (
    <div
      data-testid="listing-preview"
      className="overflow-hidden rounded-2xl border border-zinc-200 bg-white"
    >
      <div className="relative aspect-[3/2] w-full bg-zinc-100">
        {cover && (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={cover} alt="" className="absolute inset-0 h-full w-full object-cover" />
        )}
      </div>

      <div className="p-5">
        <h3 className="font-medium text-[15px] tracking-tight text-[#1c1d20]">
          {listing.address || "Адрес не указан"}
        </h3>
        <p className="mt-1.5 font-mono text-sm text-zinc-700">{money(listing.price)}</p>
        {specs && <p className="mt-0.5 font-mono text-xs text-zinc-400">{specs}</p>}
        {listing.description && (
          <p className="mt-3 line-clamp-3 text-sm text-zinc-600">{listing.description}</p>
        )}
      </div>
    </div>
  );
}
