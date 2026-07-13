"use client";
import type { CompromiseNote, RelaxationNote } from "@/lib/agent/types";

// Screen ⑤ — the honest compromises footer. Where the dossier owns its trade-offs:
// what was given up, where the search was widened, and why this zone. Renders ONLY
// the sections that carry content; if everything is empty it renders nothing at all.
export default function HonestFooter({
  compromises,
  relaxation,
  zone_rationale,
}: {
  compromises: CompromiseNote[];
  relaxation: RelaxationNote[];
  zone_rationale: string;
}) {
  const hasCompromises = compromises.length > 0;
  const hasRelaxation = relaxation.length > 0;
  const hasRationale = zone_rationale.trim().length > 0;

  if (!hasCompromises && !hasRelaxation && !hasRationale) return null;

  return (
    <footer className="border-t border-zinc-200 bg-[#fafafa]">
      <div className="mx-auto w-full max-w-5xl px-6 py-14">
        <h2 className="text-[11px] font-medium uppercase tracking-[0.2em] text-zinc-400">
          Честно о компромиссах
        </h2>

        <div className="mt-6 flex flex-col divide-y divide-zinc-200">
          {hasCompromises && (
            <section className="pb-6">
              <h3 className="flex items-center gap-2 text-sm font-medium text-[#1c1d20]">
                <svg
                  width="15" height="15" viewBox="0 0 16 16" fill="none"
                  stroke="#b3822f" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M8 4.2v4.4M8 11.2v.2M8 1.8 1.6 12.8h12.8L8 1.8Z" />
                </svg>
                На что пришлось пойти
              </h3>
              <ul className="mt-3 flex flex-col gap-2.5">
                {compromises.map((c, i) => (
                  <li key={`${c.block_key}-${i}`} className="flex gap-2.5 text-sm leading-relaxed text-zinc-600">
                    <span aria-hidden className="mt-2 h-1 w-1 shrink-0 rounded-full bg-amber-400" />
                    <span className="max-w-[70ch]">{c.text}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}

          {hasRelaxation && (
            <section className="py-6">
              <h3 className="flex items-center gap-2 text-sm font-medium text-[#1c1d20]">
                <svg
                  width="15" height="15" viewBox="0 0 16 16" fill="none"
                  stroke="#3f74b0" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M9.5 3 13 6.5 9.5 10M13 6.5H4.5M6.5 13 3 9.5" />
                </svg>
                Где расширили поиск
              </h3>
              <ul className="mt-3 flex flex-col gap-2.5">
                {relaxation.map((r, i) => (
                  <li key={i} className="flex gap-2.5 text-sm leading-relaxed text-zinc-600">
                    <span aria-hidden className="mt-2 h-1 w-1 shrink-0 rounded-full bg-sky-400" />
                    <span className="max-w-[70ch]">{r.text}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}

          {hasRationale && (
            <section className="pt-6">
              <h3 className="text-sm font-medium text-[#1c1d20]">Почему эта зона</h3>
              <p className="mt-3 max-w-[70ch] text-sm leading-relaxed text-zinc-600">
                {zone_rationale}
              </p>
            </section>
          )}
        </div>
      </div>
    </footer>
  );
}
