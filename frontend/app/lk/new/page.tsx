"use client";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import CreateWizard from "@/components/owner/CreateWizard";

function NewListingScreen() {
  const params = useSearchParams();
  // Сюда приводит запасной путь из импорта: ссылка есть, а Циан её не отдал.
  const hint = params.get("url")
    ? "Импорт с Циана не удался — заполните карточку сами, это займёт пару минут."
    : undefined;
  return <CreateWizard hint={hint} />;
}

export default function NewListingPage() {
  return (
    <Suspense fallback={<p className="text-sm text-zinc-400">Открываем форму…</p>}>
      <NewListingScreen />
    </Suspense>
  );
}
