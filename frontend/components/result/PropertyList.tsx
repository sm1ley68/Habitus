"use client";
import { motion } from "framer-motion";
import PropertyCard from "./PropertyCard";
import { useSession } from "@/lib/store/session";

export default function PropertyList() {
  const properties = useSession((s) => s.properties);
  const open = useSession((s) => s.selectProperty);
  return (
    <motion.div
      initial="hidden" animate="show"
      variants={{ show: { transition: { staggerChildren: 0.08 } } }}
      className="flex flex-col gap-3"
    >
      {properties.map((p, i) => (
        <PropertyCard key={p.id} property={p} index={i} onOpen={open} />
      ))}
    </motion.div>
  );
}
