/** Единственный источник правды по цвету. tailwind.config.ts и globals.css
 *  читают отсюда, чтобы палитра не разъехалась по трём файлам. */
export const PALETTE = {
  paper: "#F7F5F2",
  ink: "#1A1815",
  inkMuted: "#5B564E",
  inkFaint: "#8C857A",
  // Цвета происхождения факта: чем подтверждена величина, тем спокойнее цвет.
  evidence: "#14493C",
  estimate: "#9A7534",
  compromise: "#A65A43",
} as const;
