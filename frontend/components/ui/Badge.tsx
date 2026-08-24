import type { ReactNode } from "react";

export type BadgeTone = "neutral" | "ok" | "warn" | "danger";

// Палитра совпадает с оценками паспорта (lib/grade.ts): один и тот же зелёный
// означает «хорошо» и там, и здесь — продавец не переучивается.
const TONE: Record<BadgeTone, { bg: string; color: string }> = {
  neutral: { bg: "#f4f4f5", color: "#52525b" },
  ok: { bg: "#e9f5ee", color: "#2f8f5f" },
  warn: { bg: "#f8f0e0", color: "#b3822f" },
  danger: { bg: "#f6ece9", color: "#b25e4a" },
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
      className={`inline-flex items-center rounded-md px-2 py-1 text-xs ${className}`}
    >
      {children}
    </span>
  );
}
