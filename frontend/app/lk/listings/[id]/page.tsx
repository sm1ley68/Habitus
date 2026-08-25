"use client";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import ListingEditor from "@/components/owner/ListingEditor";
import { Button } from "@/components/ui";
import { getOwnerListing, OwnerApiError } from "@/lib/api/owner";
import type { OwnerListing } from "@/lib/agent/owner";

export default function ListingPage() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const id = params.id;
  const [listing, setListing] = useState<OwnerListing | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      setListing(await getOwnerListing(id));
    } catch (e) {
      setError(e instanceof OwnerApiError ? e.message : "Не удалось загрузить объявление");
    }
  }, [id]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error) {
    return (
      <div className="flex flex-col items-start gap-3 py-16">
        <p role="alert" className="text-sm text-[#b25e4a]">
          {error}
        </p>
        <Button variant="secondary" onClick={() => void load()}>
          Повторить
        </Button>
      </div>
    );
  }

  if (!listing) return <p className="py-16 text-sm text-zinc-400">Загружаем объявление…</p>;

  return <ListingEditor listing={listing} onDeleted={() => router.push("/lk")} />;
}
