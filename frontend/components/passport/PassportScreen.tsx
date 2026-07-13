"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { motion, useScroll, useSpring } from "framer-motion";
import HeroVerdict from "./dossier/HeroVerdict";
import BriefStrip from "./dossier/BriefStrip";
import Chapter from "./dossier/Chapter";
import SecondaryGrid from "./dossier/SecondaryGrid";
import HonestFooter from "./dossier/HonestFooter";
import PassportChat from "./chat/PassportChat";
import { getObjectPassport } from "@/lib/api/passport";
import type { ObjectPassport } from "@/lib/agent/types";
import { useSession } from "@/lib/store/session";

// The Lifestyle Passport as a "detective dossier": a vertical, scroll-driven
// investigation — verdict → brief → hero chapters → secondary evidence → honest
// compromises. The whole ObjectPassport now arrives through the data-seam
// (getObjectPassport) — mock today, real backend on a config flip — so the UI
// below never imports mock data directly.
export default function PassportScreen() {
  const idx = useSession((s) => s.selectedIndex);
  const property = useSession((s) => s.properties[idx]);
  const back = useSession((s) => s.setScreen);

  // TODO: real chat_id — the session store has no chat id yet, so we stand in
  // with the property id to keep the seam call-shape correct.
  const chatId = property?.id;

  const [passport, setPassport] = useState<ObjectPassport | null>(null);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");

  // Reading-progress rail, driven by the dossier's own scroll position.
  const scrollRef = useRef<HTMLDivElement>(null);
  const { scrollYProgress } = useScroll({ container: scrollRef });
  const progress = useSpring(scrollYProgress, { stiffness: 120, damping: 30, mass: 0.3 });

  const load = useCallback(() => {
    if (!property) return;
    let cancelled = false;
    setStatus("loading");
    getObjectPassport(property.id, chatId)
      .then((data) => {
        if (cancelled) return;
        setPassport(data);
        setStatus("ready");
      })
      .catch(() => {
        if (cancelled) return;
        setStatus("error");
      });
    return () => { cancelled = true; };
  }, [property, chatId]);

  useEffect(() => load(), [load]);

  if (!property) return null;

  const analysis = passport?.lifestyle_analysis;
  const heroBlocks = analysis?.blocks.filter((b) => b.tier === "hero") ?? [];
  const secondaryBlocks = analysis?.blocks.filter((b) => b.tier !== "hero") ?? [];

  return (
    <div ref={scrollRef} className="flex-1 overflow-auto">
      {/* Reading-progress rail — scales with scroll through the dossier. */}
      <motion.div
        aria-hidden
        style={{ scaleX: progress }}
        className="sticky top-0 z-30 h-[2px] origin-left bg-accent/80"
      />

      {/* Back control floats over the hero band. */}
      <div className="sticky top-0 z-20">
        <div className="mx-auto w-full max-w-5xl px-6">
          <button
            onClick={() => back("result")}
            className="mt-4 inline-flex items-center gap-1.5 rounded-full border border-white/15 bg-black/25 px-3 py-1.5 text-sm text-white/85 backdrop-blur-md transition-colors hover:text-white focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            ← К результатам
          </button>
        </div>
      </div>

      {status === "loading" && <PassportSkeleton />}

      {status === "error" && <PassportError onRetry={load} />}

      {status === "ready" && analysis && (
        <>
          <div className="-mt-[3.25rem]">
            <HeroVerdict
              property={property}
              verdict={analysis.verdict}
              matchScore={analysis.match_score}
              titleLayoutId={`property-${idx}`}
            />
          </div>

          <BriefStrip brief={analysis.brief} />

          {heroBlocks.map((block, i) => (
            <Chapter key={block.key} block={block} index={i} />
          ))}

          <SecondaryGrid blocks={secondaryBlocks} />

          <HonestFooter
            compromises={analysis.compromises}
            relaxation={analysis.relaxation}
            zone_rationale={analysis.zone_rationale}
          />

          {chatId && (
            <PassportChat
              objectId={passport.id}
              chatId={chatId}
              passport={passport}
            />
          )}
        </>
      )}
    </div>
  );
}

// Skeleton echoes the real dossier rhythm: a tall hero band, then a couple of
// chapter placeholders. Shimmer via `animate-pulse`, but `motion-reduce`
// swaps it for a static placeholder so nothing loops under reduced motion.
function PassportSkeleton() {
  return (
    <div aria-hidden className="-mt-[3.25rem]">
      {/* Hero band placeholder. */}
      <section className="relative flex min-h-[60dvh] flex-col justify-end overflow-hidden bg-[#1c1d20]">
        <div className="relative mx-auto w-full max-w-5xl px-6 pb-12 pt-24">
          <div className="grid gap-8 sm:grid-cols-[1fr_auto] sm:items-end">
            <div className="min-w-0 space-y-4">
              <div className="h-3 w-28 rounded bg-white/10 animate-pulse motion-reduce:animate-none" />
              <div className="h-9 w-2/3 rounded-lg bg-white/10 animate-pulse motion-reduce:animate-none" />
              <div className="h-5 w-3/4 rounded bg-white/10 animate-pulse motion-reduce:animate-none" />
              <div className="flex gap-4 pt-2">
                <div className="h-5 w-24 rounded bg-white/10 animate-pulse motion-reduce:animate-none" />
                <div className="h-5 w-16 rounded bg-white/10 animate-pulse motion-reduce:animate-none" />
                <div className="h-5 w-16 rounded bg-white/10 animate-pulse motion-reduce:animate-none" />
              </div>
            </div>
            <div className="h-20 w-20 rounded-full bg-white/10 animate-pulse motion-reduce:animate-none sm:justify-self-end" />
          </div>
        </div>
      </section>

      {/* Brief strip placeholder. */}
      <div className="border-t border-zinc-100 bg-white">
        <div className="mx-auto w-full max-w-5xl px-6 py-8">
          <div className="h-3 w-40 rounded bg-zinc-100 animate-pulse motion-reduce:animate-none" />
          <div className="mt-4 flex flex-wrap gap-2.5">
            {[36, 28, 24, 32, 22].map((w, i) => (
              <div
                key={i}
                className="h-8 rounded-full bg-zinc-100 animate-pulse motion-reduce:animate-none"
                style={{ width: `${w * 4}px` }}
              />
            ))}
          </div>
        </div>
      </div>

      {/* Two chapter placeholders. */}
      {[0, 1].map((i) => (
        <div key={i} className="border-t border-zinc-100">
          <div className="mx-auto grid w-full max-w-5xl gap-8 px-6 py-16 lg:grid-cols-[22rem_1fr] lg:py-24">
            <div className="space-y-3">
              <div className="h-3 w-24 rounded bg-zinc-100 animate-pulse motion-reduce:animate-none" />
              <div className="h-7 w-2/3 rounded-lg bg-zinc-100 animate-pulse motion-reduce:animate-none" />
              <div className="h-4 w-full rounded bg-zinc-100 animate-pulse motion-reduce:animate-none" />
              <div className="h-4 w-4/5 rounded bg-zinc-100 animate-pulse motion-reduce:animate-none" />
            </div>
            <div className="h-64 rounded-2xl bg-zinc-100 animate-pulse motion-reduce:animate-none" />
          </div>
        </div>
      ))}
    </div>
  );
}

function PassportError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="mx-auto flex min-h-[60dvh] w-full max-w-5xl flex-col items-start justify-center gap-4 px-6">
      <div>
        <h2 className="text-xl font-medium text-[#1c1d20]">
          Не удалось загрузить досье объекта
        </h2>
        <p className="mt-2 max-w-[52ch] text-sm leading-relaxed text-zinc-500">
          Проверьте соединение и попробуйте ещё раз. Данные объекта не были
          получены.
        </p>
      </div>
      <button
        type="button"
        onClick={onRetry}
        className="inline-flex items-center gap-1.5 rounded-full border border-zinc-300 bg-white px-4 py-2 text-sm font-medium text-[#1c1d20] transition-transform hover:bg-zinc-50 active:scale-[0.98] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Повторить
      </button>
    </div>
  );
}
