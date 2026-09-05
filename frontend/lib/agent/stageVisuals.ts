import type { Stage } from "./types";
import { PALETTE } from "@/lib/tokens";

// Свечение края экрана на разных стадиях агента (--glow-color в globals.css).
// Одно свечение на все стадии — акцент интерфейса, а не светофор. Разноцветные
// стадии (голубая «гео», сиреневая «контекст») были языком «ИИ работает», из
// которого продукт уходит: человеку не нужно знать, какой агент сейчас думает,
// ему нужно видеть, что процесс идёт. Отдельные оттенки к тому же тащили в
// интерфейс сиреневый #9B8CFF — тот же барвинок, что и удалённый #7C8CFF,
// только другим кодом, поэтому греп по старым значениям его не ловил.
export const STAGE_GLOW: Record<Stage, { color: string; opacity: number; caption: string }> = {
  idle:       { color: PALETTE.evidence, opacity: 0,    caption: "" },
  linguistic: { color: PALETTE.evidence, opacity: 1,    caption: "Разбираю запрос…" },
  geo:        { color: PALETTE.evidence, opacity: 1,    caption: "Строю маршруты…" },
  context:    { color: PALETTE.evidence, opacity: 1,    caption: "Смотрю район…" },
  relaxation: { color: PALETTE.evidence, opacity: 0.85, caption: "Смягчаю критерии…" },
  streaming:  { color: PALETTE.evidence, opacity: 0,    caption: "Собираю ответ…" },
  done:       { color: PALETTE.evidence, opacity: 0,    caption: "" },
  error:      { color: PALETTE.inkFaint, opacity: 0.6,  caption: "" },
};
