"use client";

import { motion, useReducedMotion } from "framer-motion";
import StageCaption from "./StageCaption";
import { DUR, EASE } from "@/lib/motion";
import type { Stage } from "@/lib/agent/types";

// Стадии, во время которых человек ждёт ответа: сюда же входит relaxation —
// смягчение критериев не прерывает ожидание, а сопровождает его (карточку
// рисует MessageThread отдельно, чтобы не задваивать сообщение на экране).
export const THINKING: Stage[] = ["linguistic", "geo", "context", "relaxation", "streaming"];

export function isThinking(stage: Stage) {
  return THINKING.includes(stage);
}

/**
 * Замена «радара»: вместо имитации работы карты — то, что реально есть —
 * собственная просьба человека и спокойный статус стадии. Пропов нет: как и
 * у GeoThinkingCanvas раньше, компонент сам читает стадию из useSession;
 * исходную реплику ему передаёт MessageThread, у которого есть searchMessages.
 */
export default function SearchWaiting({ request }: { request?: string }) {
  const reduce = useReducedMotion();
  return (
    <motion.div
      initial={reduce ? false : { opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: DUR.base, ease: EASE.standard }}
      className="mx-auto w-full max-w-[540px] rounded-lg border border-black/[0.08] bg-white p-6"
    >
      {request && (
        <p className="font-display text-lg leading-snug text-ink">«{request}»</p>
      )}
      <div className="mt-5 border-t border-black/[0.06] pt-4">
        <StageCaption />
      </div>
      {/* Одна тонкая линия вместо процентов: честного процента у нас нет —
          сколько осталось до ответа, пайплайн не знает. */}
      {!reduce && (
        <div aria-hidden className="mt-4 h-px w-full overflow-hidden bg-black/[0.06]">
          <div className="h-full w-1/3 animate-[waiting_1.8s_ease-in-out_infinite] bg-ink-faint" />
        </div>
      )}
    </motion.div>
  );
}
