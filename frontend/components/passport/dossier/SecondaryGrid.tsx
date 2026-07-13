"use client";
import { motion, useReducedMotion } from "framer-motion";
import PassportIcon from "../PassportIcon";
import GradeBadge from "../GradeBadge";
import { VIZ_REGISTRY } from "../viz";
import { SPRING } from "@/lib/motion";
import type { LifestyleBlock as Block } from "@/lib/agent/types";

// Screen ④ — the secondary evidence grid. Compact two-up cards for the blocks that
// aren't full chapters: icon + title + grade, then the small registered viz if one
// exists, else the plain description.
export default function SecondaryGrid({ blocks }: { blocks: Block[] }) {
  const reduce = useReducedMotion();
  if (!blocks.length) return null;

  return (
    <section className="border-t border-zinc-100 bg-white">
      <div className="mx-auto w-full max-w-5xl px-6 py-12">
        <h2 className="text-[11px] font-medium uppercase tracking-[0.2em] text-zinc-400">
          Прочие слои
        </h2>
        <motion.div
          initial="hidden"
          whileInView="show"
          viewport={{ once: true, margin: "-40px" }}
          variants={{ show: { transition: { staggerChildren: reduce ? 0 : 0.07 } } }}
          className="mt-5 grid gap-4 md:grid-cols-2"
        >
          {blocks.map((block) => {
            const Viz = VIZ_REGISTRY[block.key];
            return (
              <motion.div
                key={block.key}
                variants={{ hidden: reduce ? {} : { opacity: 0, y: 12 }, show: { opacity: 1, y: 0 } }}
                transition={SPRING.soft}
                className="rounded-2xl border border-zinc-200 p-5"
              >
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2.5">
                    <span className="text-zinc-500">
                      <PassportIcon name={block.icon} />
                    </span>
                    <h3 className="font-medium text-[#1c1d20]">{block.title}</h3>
                  </div>
                  <GradeBadge score={block.score} />
                </div>
                {Viz && block.metrics ? (
                  <div className="mt-4">
                    <Viz
                      metrics={block.metrics}
                      waypoints={block.waypoints}
                      destinations={block.destinations}
                      data={block.data}
                    />
                  </div>
                ) : null}
                <p className="mt-3 text-sm leading-relaxed text-zinc-600">
                  {block.description}
                </p>
              </motion.div>
            );
          })}
        </motion.div>
      </div>
    </section>
  );
}
