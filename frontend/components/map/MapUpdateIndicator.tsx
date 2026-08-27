"use client";

import { AnimatePresence, motion, useReducedMotion } from "framer-motion";

export default function MapUpdateIndicator({ visible }: { visible: boolean }) {
  const reduceMotion = useReducedMotion();
  return (
    <AnimatePresence>
      {visible && (
        <motion.div
          role="status"
          aria-live="polite"
          initial={reduceMotion ? false : { opacity: 0, y: -6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={reduceMotion ? { opacity: 0 } : { opacity: 0, y: -4 }}
          className="pointer-events-none absolute right-4 top-4 z-20 flex items-center gap-3 rounded-full border border-accent/35 bg-[#eeeafe]/85 px-5 py-3.5 text-sm font-medium text-[#5f55b8] shadow-[0_12px_34px_-16px_rgba(95,85,184,0.55)] backdrop-blur-sm"
        >
          <span
            aria-hidden
            className={`h-5 w-5 rounded-full border-[2.5px] border-accent/25 border-t-accent ${
              reduceMotion ? "" : "animate-spin"
            }`}
          />
          Обновляю область
        </motion.div>
      )}
    </AnimatePresence>
  );
}
