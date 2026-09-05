"use client";
import { motion } from "framer-motion";
import { SPRING } from "@/lib/motion";
import Provenance from "@/components/ui/Provenance";
export default function RelaxationCard() {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}
      transition={SPRING.soft}
      className="max-w-xl mx-auto rounded-2xl border border-zinc-200 bg-zinc-50 px-5 py-4 text-sm text-zinc-600"
    >
      {/* Метка вплетена в предложение, а не приклеена сбоку значком: это
          ослабленное условие — ровно тот смысл, который словарь проекта
          называет «компромисс» (Provenance kind="compromise"). */}
      <Provenance kind="compromise" className="mr-1.5 align-middle" />
      Немного смягчил критерии, чтобы не потерять сильные варианты рядом. Показываю то, что почти совпадает.
    </motion.div>
  );
}
