"use client";
import { motion } from "framer-motion";
import PassportIcon from "./PassportIcon";
import GradeBadge from "./GradeBadge";
import { VIZ_REGISTRY } from "./viz";
import { SPRING } from "@/lib/motion";
import type { LifestyleBlock as Block } from "@/lib/agent/types";

export default function LifestyleBlock({ block }: { block: Block }) {
  const Viz = VIZ_REGISTRY[block.key];
  return (
    <motion.div
      variants={{ hidden: { opacity: 0, y: 12 }, show: { opacity: 1, y: 0 } }}
      transition={SPRING.soft}
      className="flex items-start gap-4 border-t border-zinc-100 py-5"
    >
      <span className="text-zinc-500 mt-0.5"><PassportIcon name={block.icon} /></span>
      <div className="flex-1">
        <div className="flex items-center justify-between gap-3">
          <h3 className="font-medium text-[#1c1d20]">{block.title}</h3>
          <GradeBadge score={block.score} />
        </div>
        {Viz && block.metrics && (
          <div data-testid="viz" className="mt-3">
            <Viz metrics={block.metrics} waypoints={block.waypoints} destinations={block.destinations} />
          </div>
        )}
        <p className="mt-2 text-sm text-zinc-600 leading-relaxed max-w-[65ch]">{block.description}</p>
      </div>
    </motion.div>
  );
}
