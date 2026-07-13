"use client";
import { AnimatePresence, motion } from "framer-motion";
import { useSession } from "@/lib/store/session";
import { STAGE_GLOW } from "@/lib/agent/stageVisuals";
import { DUR } from "@/lib/motion";

export default function StageCaption() {
  const stage = useSession((s) => s.stage);
  const caption = STAGE_GLOW[stage].caption;
  return (
    <AnimatePresence mode="wait">
      {caption && (
        <motion.p
          key={stage}
          initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }}
          transition={{ duration: DUR.base }}
          className="text-sm text-zinc-500 text-center"
        >
          {caption}
        </motion.p>
      )}
    </AnimatePresence>
  );
}
