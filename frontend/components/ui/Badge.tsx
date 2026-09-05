import type { ReactNode } from "react";
import { PALETTE } from "@/lib/tokens";

export type BadgeTone = "neutral" | "ok" | "warn" | "danger";

// Тон совпадает с происхождением факта: один и тот же зелёный означает
// «подтверждено» и в бейдже статуса, и в досье — пользователь не переучивается.
const TONE: Record<BadgeTone, { bg: string; color: string }> = {
  neutral: { bg: "#EFECE7", color: PALETTE.inkMuted },
  ok:      { bg: "#E7EFEA", color: PALETTE.evidence },
  warn:    { bg: "#F5EEDF", color: PALETTE.estimate },
  danger:  { bg: "#F3E7E3", color: PALETTE.compromise },
};

/**
 * Статус всегда подписан словом: цвет здесь — усиление, а не носитель смысла.
 */
export default function Badge({
  tone = "neutral", className = "", children,
}: { tone?: BadgeTone; className?: string; children: ReactNode }) {
  const { bg, color } = TONE[tone];
  return (
    <span
      style={{ backgroundColor: bg, color }}
      className={`inline-flex items-center rounded px-2 py-1 text-xs ${className}`}
    >
      {children}
    </span>
  );
}
