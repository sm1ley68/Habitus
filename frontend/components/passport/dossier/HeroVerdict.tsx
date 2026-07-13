"use client";
import { motion, useReducedMotion } from "framer-motion";
import MatchScore from "@/components/result/MatchScore";
import { money } from "@/lib/format";
import { DUR, EASE, SPRING } from "@/lib/motion";
import type { Property, VerdictInfo } from "@/lib/agent/types";

// Screen ① — the cinematic verdict band. A muted cover backdrop under a dark wash
// (never pure black — #1c1d20), the property name (shared-element target), the
// one-line verdict, the hard essentials in mono, the match ring, and a
// «Проверено N слоёв» credibility chip. Full-bleed, min 60dvh.
export default function HeroVerdict({
  property,
  verdict,
  matchScore,
  titleLayoutId,
}: {
  property: Property;
  verdict: VerdictInfo;
  matchScore: number;
  titleLayoutId?: string;
}) {
  const reduce = useReducedMotion();
  const rise = (delay: number) =>
    reduce
      ? {}
      : {
          initial: { opacity: 0, y: 14 },
          animate: { opacity: 1, y: 0 },
          transition: { ...SPRING.soft, delay },
        };

  return (
    <section className="relative flex min-h-[60dvh] flex-col justify-end overflow-hidden bg-[#1c1d20] text-white">
      {/* Muted cover backdrop, drifting slowly (Ken Burns) so the band breathes. */}
      <motion.img
        src={property.cover_image}
        alt=""
        aria-hidden
        className="absolute inset-0 h-full w-full object-cover opacity-40 will-change-transform"
        initial={reduce ? false : { scale: 1.04, x: "-1%", y: "-1%" }}
        animate={reduce ? undefined : { scale: 1.16, x: "1.5%", y: "1.5%" }}
        transition={reduce ? undefined : { duration: 26, ease: "linear", repeat: Infinity, repeatType: "reverse" }}
      />
      <div
        aria-hidden
        className="absolute inset-0 bg-gradient-to-t from-[#1c1d20] via-[#1c1d20]/85 to-[#1c1d20]/40"
      />
      <div
        aria-hidden
        className="absolute inset-0 bg-[radial-gradient(120%_90%_at_15%_100%,rgba(124,140,255,0.16),transparent_60%)]"
      />
      {/* A slow raking light sweep — the accent catching the dossier. */}
      {!reduce && (
        <motion.div
          aria-hidden
          className="pointer-events-none absolute inset-y-0 w-1/2 mix-blend-screen"
          style={{
            background:
              "linear-gradient(105deg, transparent 0%, rgba(124,140,255,0.10) 45%, rgba(255,255,255,0.06) 50%, rgba(124,140,255,0.10) 55%, transparent 100%)",
          }}
          initial={{ x: "-120%" }}
          animate={{ x: "260%" }}
          transition={{ duration: 9, ease: EASE.standard, repeat: Infinity, repeatDelay: 4 }}
        />
      )}

      <div className="relative mx-auto w-full max-w-5xl px-6 pb-12 pt-24">
        <div className="grid gap-8 sm:grid-cols-[1fr_auto] sm:items-end">
          <div className="min-w-0">
            <motion.p
              {...rise(0)}
              className="text-[11px] font-medium uppercase tracking-[0.2em] text-white/50"
            >
              Досье объекта
            </motion.p>
            <motion.h1
              layoutId={titleLayoutId}
              className="mt-2 text-3xl font-medium tracking-tight sm:text-4xl"
            >
              {property.name}
            </motion.h1>
            <motion.p
              {...rise(0.06)}
              className="mt-4 max-w-[42ch] text-lg font-medium leading-snug text-white/90 sm:text-xl"
            >
              {verdict.headline}
            </motion.p>

            <motion.dl
              {...rise(0.12)}
              className="mt-6 flex flex-wrap items-baseline gap-x-6 gap-y-2 text-sm text-white/70"
            >
              <div className="flex items-baseline gap-1.5">
                <dt className="sr-only">Цена от</dt>
                <dd className="font-mono text-base font-medium text-white">
                  {money(property.price_from)}
                </dd>
              </div>
              <div className="flex items-baseline gap-1.5">
                <dt className="sr-only">Комнат</dt>
                <dd className="font-mono">{property.rooms}-комн</dd>
              </div>
              <div className="flex items-baseline gap-1.5">
                <dt className="sr-only">Площадь</dt>
                <dd className="font-mono">{property.area_sqm} м²</dd>
              </div>
              <div className="flex items-baseline gap-1.5">
                <dt className="sr-only">Этаж</dt>
                <dd className="font-mono">{property.floor} этаж</dd>
              </div>
            </motion.dl>

            <motion.div {...rise(0.18)} className="mt-6">
              <span className="inline-flex items-center gap-1.5 rounded-full border border-white/15 bg-white/5 px-3 py-1 text-xs font-medium text-white/80 backdrop-blur-sm">
                <svg
                  width="13"
                  height="13"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke="#7C8CFF"
                  strokeWidth="1.6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M8 1.5 2 4v3.5c0 3.4 2.4 6 6 7 3.6-1 6-3.6 6-7V4L8 1.5Z" />
                  <path d="M5.5 8 7.3 9.8 10.8 6" />
                </svg>
                Проверено <span className="font-mono">{verdict.layers_checked}</span> слоёв
              </span>
            </motion.div>
          </div>

          <motion.div
            initial={reduce ? false : { opacity: 0, scale: 0.9 }}
            animate={{ opacity: 1, scale: 1 }}
            transition={reduce ? { duration: 0 } : { duration: DUR.slow, ease: EASE.emphasizedDecelerate, delay: 0.1 }}
            className="flex items-center gap-3 sm:flex-col sm:items-end"
          >
            <MatchScore value={matchScore} />
            <span className="text-xs font-medium uppercase tracking-wider text-white/45">
              совпадение
            </span>
          </motion.div>
        </div>

        {/* Scroll cue — invites the reader down into the investigation. */}
        <motion.div
          {...(reduce ? {} : { initial: { opacity: 0 }, animate: { opacity: 1 }, transition: { delay: 0.6, duration: DUR.slow } })}
          className="mt-10 flex items-center gap-2 text-[11px] font-medium uppercase tracking-[0.2em] text-white/40"
        >
          <span>Листайте досье</span>
          <motion.svg
            width="14" height="14" viewBox="0 0 16 16" fill="none"
            stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"
            aria-hidden="true"
            animate={reduce ? undefined : { y: [0, 3, 0] }}
            transition={reduce ? undefined : { duration: 1.6, ease: EASE.standard, repeat: Infinity }}
          >
            <path d="M8 3v10M4 9l4 4 4-4" />
          </motion.svg>
        </motion.div>
      </div>
    </section>
  );
}
