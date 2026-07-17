"use client";
import { motion, useReducedMotion, type Variants } from "framer-motion";
import PassportIcon from "../PassportIcon";
import GradeBadge from "../GradeBadge";
import { VIZ_REGISTRY } from "../viz";
import { DUR, EASE, SPRING } from "@/lib/motion";
import type { LifestyleBlock as Block } from "@/lib/agent/types";

// Screen ③ — an investigation chapter. Asymmetric split: a sticky left "clue"
// column (chapter kicker, icon, title, grade, verdict line, hard metrics in mono)
// over a faint editorial ghost-number, and a right column holding the hero
// instrument, driven by block.data. The clue column reveals as a staggered
// cascade; the instrument rises in beside it. Height follows content — no dead
// full-screen gaps between chapters.

const clueGroup: Variants = {
  hidden: {},
  show: { transition: { staggerChildren: 0.07, delayChildren: 0.04 } },
};
const clueItem: Variants = {
  hidden: { opacity: 0, y: 14 },
  show: { opacity: 1, y: 0, transition: { duration: DUR.slow, ease: EASE.emphasizedDecelerate } },
};

export default function Chapter(
  { block, index, home }: { block: Block; index: number; home?: [number, number] },
) {
  const reduce = useReducedMotion();
  const Viz = VIZ_REGISTRY[block.key];
  const metrics = block.metrics ? Object.entries(block.metrics) : [];
  const num = String(index + 1).padStart(2, "0");

  return (
    <section className="relative border-t border-zinc-100">
      {/* Editorial ghost number — a large, near-invisible index behind the clue. */}
      <span
        aria-hidden
        className="pointer-events-none absolute left-2 top-8 select-none font-mono text-[6.5rem] font-semibold leading-none text-zinc-900/[0.035] sm:left-4 lg:text-[9rem]"
      >
        {num}
      </span>

      <div className="relative mx-auto grid w-full max-w-5xl gap-8 px-6 py-14 lg:grid-cols-[22rem_1fr] lg:items-start lg:py-20">
        {/* Clue column — sticky on wide screens, staggered reveal. */}
        <motion.div
          variants={reduce ? undefined : clueGroup}
          initial={reduce ? false : "hidden"}
          whileInView={reduce ? undefined : "show"}
          viewport={{ once: true, margin: "-100px" }}
          className="lg:sticky lg:top-16"
        >
          {/* Kicker: ГЛАВА NN · growing hairline · springing icon. */}
          <motion.div variants={clueItem} className="flex items-center gap-2.5 text-zinc-400">
            <span className="font-mono text-[11px] uppercase tracking-[0.18em]">
              Глава {num}
            </span>
            <motion.span
              className="h-px origin-left bg-gradient-to-r from-zinc-300 to-transparent"
              style={{ width: "3.5rem" }}
              initial={reduce ? false : { scaleX: 0 }}
              whileInView={reduce ? undefined : { scaleX: 1 }}
              viewport={{ once: true, margin: "-100px" }}
              transition={{ duration: DUR.cinematic, ease: EASE.emphasizedDecelerate, delay: 0.12 }}
            />
            <motion.span
              className="text-accent"
              initial={reduce ? false : { opacity: 0, rotate: -14, scale: 0.6 }}
              whileInView={reduce ? undefined : { opacity: 1, rotate: 0, scale: 1 }}
              viewport={{ once: true, margin: "-100px" }}
              transition={{ ...SPRING.snappy, delay: 0.22 }}
            >
              <PassportIcon name={block.icon} />
            </motion.span>
          </motion.div>

          <motion.div variants={clueItem} className="mt-3 flex items-start justify-between gap-3">
            <h2 className="text-2xl font-medium tracking-tight text-[#1c1d20]">
              {block.title}
            </h2>
            <GradeBadge score={block.score} />
          </motion.div>

          {block.verdict_line && (
            <motion.p
              variants={clueItem}
              className="mt-3 max-w-[38ch] text-[15px] leading-relaxed text-zinc-600"
            >
              {block.verdict_line}
            </motion.p>
          )}

          {metrics.length > 0 && (
            <motion.dl
              variants={reduce ? undefined : clueGroup}
              className="mt-5 flex flex-col gap-2 border-t border-zinc-100 pt-4"
            >
              {metrics.map(([k, v]) => (
                <motion.div
                  key={k}
                  variants={clueItem}
                  className="group flex items-baseline justify-between gap-4"
                >
                  <dt className="text-xs text-zinc-400 transition-colors duration-200 group-hover:text-zinc-600">
                    {k}
                  </dt>
                  <dd className="font-mono text-sm text-[#1c1d20]">
                    <span className="inline-block transition-transform duration-300 ease-out group-hover:-translate-y-px">
                      {String(v)}
                    </span>
                  </dd>
                </motion.div>
              ))}
            </motion.dl>
          )}
        </motion.div>

        {/* Instrument column. */}
        <motion.div
          initial={reduce ? false : { opacity: 0, y: 28, scale: 0.985 }}
          whileInView={{ opacity: 1, y: 0, scale: 1 }}
          viewport={{ once: true, margin: "-80px" }}
          transition={reduce ? { duration: 0 } : { duration: DUR.slow, ease: EASE.emphasizedDecelerate, delay: 0.08 }}
          className="min-w-0"
        >
          {Viz ? (
            <Viz
              metrics={block.metrics ?? {}}
              waypoints={block.waypoints}
              destinations={block.destinations}
              data={block.data}
              home={home}
            />
          ) : (
            <p className="text-sm leading-relaxed text-zinc-600">{block.description}</p>
          )}
          {Viz && (
            <motion.p
              initial={reduce ? false : { opacity: 0 }}
              whileInView={reduce ? undefined : { opacity: 1 }}
              viewport={{ once: true, margin: "-60px" }}
              transition={{ duration: DUR.slow, delay: 0.25 }}
              className="mt-4 max-w-[60ch] text-sm leading-relaxed text-zinc-600"
            >
              {block.description}
            </motion.p>
          )}
        </motion.div>
      </div>
    </section>
  );
}
