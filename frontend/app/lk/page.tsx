"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import ListingsList from "@/components/owner/ListingsList";
import { Button } from "@/components/ui";
import { listOwnerListings, OwnerApiError } from "@/lib/api/owner";
import type { OwnerListing } from "@/lib/agent/owner";

export default function CabinetPage() {
  const [listings, setListings] = useState<OwnerListing[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      setListings(await listOwnerListings());
    } catch (e) {
      setError(e instanceof OwnerApiError ? e.message : "Не удалось загрузить объявления");
    }
  }, []);

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

  if (listings === null) {
    return <p className="py-16 text-sm text-zinc-400">Загружаем объявления…</p>;
  }

  return (
    <div className="flex flex-col gap-6">
      {listings.length > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-zinc-500">
            {listings.length}{" "}
            {listings.length === 1 ? "объявление" : listings.length < 5 ? "объявления" : "объявлений"}
          </p>
          <div className="flex gap-2">
            <Link href="/lk/new">
              <Button variant="secondary">Заполнить вручную</Button>
            </Link>
            <Link href="/lk/import">
              <Button>Перенести с Циана</Button>
            </Link>
          </div>
        </div>
      )}

      <ListingsList listings={listings} onChanged={() => void load()} />
    </div>
  );
}
