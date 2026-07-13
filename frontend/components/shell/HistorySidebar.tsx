"use client";
import { AnimatePresence, motion } from "framer-motion";
import { useSession } from "@/lib/store/session";
import { HISTORY } from "@/lib/data/mock";
import { SPRING } from "@/lib/motion";
import CitySwitch from "./CitySwitch";
export default function HistorySidebar() {
  const open = useSession((s) => s.historyOpen);
  return (
    <AnimatePresence>
      {open && (
        <motion.aside
          initial={{ x: -280, opacity: 0 }} animate={{ x: 0, opacity: 1 }} exit={{ x: -280, opacity: 0 }}
          transition={SPRING.gentle}
          className="w-72 shrink-0 border-r border-zinc-100 p-4 flex flex-col gap-4 bg-white z-[2]">
          <CitySwitch />
          <div className="flex flex-col gap-1">
            {HISTORY.map((h) => (
              <button key={h.title} className="text-left rounded-lg px-3 py-2 hover:bg-zinc-100">
                <p className="text-sm text-[#1c1d20] line-clamp-1">{h.title}</p>
                <p className="text-xs text-zinc-400">{h.time}</p>
              </button>
            ))}
          </div>
        </motion.aside>
      )}
    </AnimatePresence>
  );
}
