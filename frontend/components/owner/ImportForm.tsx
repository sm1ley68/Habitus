"use client";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { Button, Field, Input } from "@/components/ui";
import { importListing, OwnerApiError, previewImport } from "@/lib/api/owner";
import type { ImportPreview } from "@/lib/agent/owner";
import CianUnavailable from "./CianUnavailable";
import ImportPreviewCard from "./ImportPreviewCard";
import SimilarWarning from "./SimilarWarning";

/**
 * Экран импорта. Ошибки разводятся по коду, а не по тексту: недоступный Циан —
 * это тупик с обходным путём, а чужая или кривая ссылка — то, что продавец
 * может исправить прямо здесь.
 */
export default function ImportForm({ initialUrl = "" }: { initialUrl?: string }) {
  const router = useRouter();
  const [url, setUrl] = useState(initialUrl);
  const [checking, setChecking] = useState(false);
  const [importing, setImporting] = useState(false);
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState<string | null>(null);

  const check = async (e: FormEvent) => {
    e.preventDefault();
    setChecking(true);
    setError(null);
    setUnavailable(null);
    setPreview(null);
    try {
      setPreview(await previewImport(url.trim()));
    } catch (e) {
      if (e instanceof OwnerApiError && e.code === "cian_unavailable") setUnavailable(e.message);
      else setError(e instanceof OwnerApiError ? e.message : "Не удалось проверить ссылку");
    } finally {
      setChecking(false);
    }
  };

  const confirm = async () => {
    setImporting(true);
    setError(null);
    try {
      const created = await importListing(url.trim());
      router.push(`/lk/listings/${created.id}`);
    } catch (e) {
      if (e instanceof OwnerApiError && e.code === "cian_unavailable") setUnavailable(e.message);
      else setError(e instanceof OwnerApiError ? e.message : "Не удалось импортировать объявление");
    } finally {
      setImporting(false);
    }
  };

  return (
    <div className="flex max-w-xl flex-col gap-6">
      <div>
        <h1 className="text-xl tracking-tight text-[#1c1d20]">Перенести объявление с Циана</h1>
        <p className="mt-2 text-sm text-zinc-500">
          Вставьте ссылку на страницу объявления — данные подтянутся сами, останется
          только проверить.
        </p>
      </div>

      <form onSubmit={check} className="flex flex-col gap-3">
        <Field label="Ссылка на объявление с Циана" hint="Например, https://www.cian.ru/sale/flat/318394906/">
          <Input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            inputMode="url"
            autoComplete="off"
          />
        </Field>
        <div>
          <Button type="submit" loading={checking} disabled={url.trim().length === 0}>
            Проверить
          </Button>
        </div>
      </form>

      {error && (
        <p role="alert" className="text-sm text-[#b25e4a]">
          {error}
        </p>
      )}

      {unavailable && <CianUnavailable message={unavailable} url={url.trim()} />}

      {preview && (
        <>
          {/* Похожие рисуются поверх любого вердикта и ничего не блокируют. */}
          <SimilarWarning similar={preview.similar} />
          <ImportPreviewCard preview={preview} busy={importing} onConfirm={() => void confirm()} />
        </>
      )}
    </div>
  );
}
