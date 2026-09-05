import type { Stage } from "./types";
import { PALETTE } from "@/lib/tokens";

// Свечение края экрана на разных стадиях агента (--glow-color в globals.css).
// Стадии без собственного цвета (idle/streaming/done, opacity 0 — свечения не
// видно) и «обычные» стадии мышления (linguistic/relaxation) держатся на
// PALETTE.evidence — это акцент интерфейса, а не сигнал происхождения факта,
// поэтому geo/context/error намеренно остаются на своих узнаваемых оттенках.
export const STAGE_GLOW: Record<Stage, { color: string; opacity: number; caption: string }> = {
  idle:       { color: PALETTE.evidence, opacity: 0,    caption: "" },
  linguistic: { color: PALETTE.evidence, opacity: 1,    caption: "Разбираю запрос…" },
  geo:        { color: "#5AB8E0", opacity: 1,    caption: "Строю маршруты…" },
  context:    { color: "#9B8CFF", opacity: 1,    caption: "Смотрю район…" },
  relaxation: { color: PALETTE.evidence, opacity: 0.85, caption: "Смягчаю критерии…" },
  streaming:  { color: PALETTE.evidence, opacity: 0,    caption: "Собираю ответ…" },
  done:       { color: PALETTE.evidence, opacity: 0,    caption: "" },
  error:      { color: "#9BAAB8", opacity: 0.6,  caption: "" },
};
