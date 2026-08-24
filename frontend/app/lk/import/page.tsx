"use client";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import ImportForm from "@/components/owner/ImportForm";

// useSearchParams требует границы Suspense, иначе страница не пререндерится.
function ImportScreen() {
  const params = useSearchParams();
  return <ImportForm initialUrl={params.get("url") ?? ""} />;
}

export default function ImportPage() {
  return (
    <Suspense fallback={<p className="text-sm text-zinc-400">Открываем импорт…</p>}>
      <ImportScreen />
    </Suspense>
  );
}
