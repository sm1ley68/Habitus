"use client";
import Provenance, { provenanceOfSource } from "@/components/ui/Provenance";
import type { BlockSource, SourceKind } from "@/lib/agent/types";

const KIND_LABEL: Record<SourceKind, string> = {
  observation: "наблюдение",
  computation: "вычисление",
  proxy: "оценка по модели",
};

// Строгость по возрастанию. Плашку заслуживает только прокси: вычисление —
// нормальный режим работы продукта, и значок на нём обесценил бы значок на
// модели. Если помечено всё, не помечено ничто.
const SEVERITY: Record<SourceKind, number> = {
  observation: 0, computation: 1, proxy: 2,
};

export function worstKind(sources: BlockSource[]): SourceKind | null {
  if (!sources.length) return null;
  return sources.reduce<SourceKind>(
    (worst, s) => (SEVERITY[s.kind] > SEVERITY[worst] ? s.kind : worst),
    "observation",
  );
}

function when(iso?: string | null): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? null
    : d.toLocaleDateString("ru-RU", { month: "short", year: "numeric" });
}

export function ProxyBadge({ sources }: { sources?: BlockSource[] }) {
  if (worstKind(sources ?? []) !== "proxy") return null;
  return (
    <span className="rounded-full bg-[#f8f0e0] px-2 py-0.5 text-[11px] text-[#b3822f]">
      оценка по модели
    </span>
  );
}

export default function BlockSources({ sources }: { sources?: BlockSource[] }) {
  if (!sources?.length) return null;
  return (
    <ul className="mt-4 flex flex-col gap-1.5 border-t border-black/[0.06] pt-3">
      {sources.map((s) => {
        const date = when(s.observed_at);
        return (
          <li key={s.key} className="text-xs leading-relaxed text-ink-faint">
            {/* Название источника и его происхождение (замер/модель/...) —
                тот же словарь, что красит плечи маршрута и другие факты
                продукта: один язык происхождения на весь фронт.
                inline-flex, а не flex: это строчный элемент внутри <li>,
                blockовый flex порвал бы поток и увёл «— вид, основание» на
                отдельную строку. */}
            <span className="inline-flex items-center gap-2 text-ink-muted">
              {s.label}{" "}
              <Provenance kind={provenanceOfSource(s.kind)} />
            </span>{" "}
            — {KIND_LABEL[s.kind]},{" "}
            {s.basis}
            {date && <span> · {date}</span>}
          </li>
        );
      })}
    </ul>
  );
}
