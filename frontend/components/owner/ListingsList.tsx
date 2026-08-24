import type { OwnerListing } from "@/lib/agent/owner";
import EmptyCabinet from "./EmptyCabinet";
import ListingRow from "./ListingRow";

export default function ListingsList({
  listings, onChanged,
}: { listings: OwnerListing[]; onChanged: () => void }) {
  if (listings.length === 0) return <EmptyCabinet />;

  return (
    <ul className="flex flex-col gap-3">
      {listings.map((listing) => (
        <li key={listing.id}>
          <ListingRow listing={listing} onChanged={onChanged} />
        </li>
      ))}
    </ul>
  );
}
