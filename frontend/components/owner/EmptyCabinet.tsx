"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { Button, Input } from "@/components/ui";

/**
 * Пустой кабинет — это приглашение к действию, а не заглушка. Главный путь
 * (ссылка с Циана) стоит первым и крупным, ручное заполнение — тихой ссылкой.
 */
export default function EmptyCabinet() {
  const router = useRouter();
  const [url, setUrl] = useState("");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = url.trim();
    router.push(trimmed ? `/lk/import?url=${encodeURIComponent(trimmed)}` : "/lk/import");
  };

  return (
    <div className="mx-auto flex w-full max-w-xl flex-col items-center gap-6 py-16 text-center">
      <div>
        <h2 className="text-xl tracking-tight text-[#1c1d20]">Здесь появятся ваши объявления</h2>
        <p className="mt-2 text-sm text-zinc-500">
          Быстрее всего начать со ссылки на объявление с Циана — данные подтянутся сами.
        </p>
      </div>

      <form onSubmit={submit} className="flex w-full flex-col gap-3 sm:flex-row">
        <Input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://www.cian.ru/sale/flat/318394906/"
          aria-label="Ссылка на объявление с Циана"
          inputMode="url"
        />
        <Button type="submit" className="sm:w-auto">
          Перенести
        </Button>
      </form>

      <Link href="/lk/new" className="text-sm text-zinc-400 transition-colors hover:text-zinc-600">
        или заполнить вручную
      </Link>
    </div>
  );
}
