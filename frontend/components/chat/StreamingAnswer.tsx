"use client";
import { useSession } from "@/lib/store/session";
export default function StreamingAnswer() {
  const answer = useSession((s) => s.answer);
  const streaming = useSession((s) => s.stage === "streaming");
  if (!answer) return null;
  return (
    <p className="max-w-xl mx-auto text-[15px] leading-relaxed text-[#1c1d20]">
      {answer}
      {streaming && <span className="inline-block w-[2px] h-[1.1em] align-[-2px] ml-0.5 bg-accent animate-pulse" />}
    </p>
  );
}
