import type { Stage } from "./types";

export const STAGE_GLOW: Record<Stage, { color: string; opacity: number; caption: string }> = {
  idle:       { color: "#7C8CFF", opacity: 0,    caption: "" },
  linguistic: { color: "#7C8CFF", opacity: 1,    caption: "Разбираю запрос…" },
  geo:        { color: "#5AB8E0", opacity: 1,    caption: "Строю маршруты…" },
  context:    { color: "#9B8CFF", opacity: 1,    caption: "Смотрю район…" },
  relaxation: { color: "#7C8CFF", opacity: 0.85, caption: "Смягчаю критерии…" },
  streaming:  { color: "#7C8CFF", opacity: 0,    caption: "Собираю ответ…" },
  done:       { color: "#7C8CFF", opacity: 0,    caption: "" },
  error:      { color: "#9BAAB8", opacity: 0.6,  caption: "" },
};
