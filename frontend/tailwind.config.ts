import type { Config } from "tailwindcss";
import { PALETTE } from "./lib/tokens";

const config: Config = {
  content: [
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      fontFamily: {
        display: ["var(--font-display)", "Georgia", "serif"],
        sans: ["var(--font-ui)", "system-ui", "sans-serif"],
        mono: ["var(--font-mono)", "monospace"],
      },
      colors: {
        paper: PALETTE.paper,
        ink: { DEFAULT: PALETTE.ink, muted: PALETTE.inkMuted, faint: PALETTE.inkFaint },
        evidence: PALETTE.evidence,
        estimate: PALETTE.estimate,
        compromise: PALETTE.compromise,
        // accent — не из палитры задачи 1: примерно два десятка компонентов
        // (Composer, чаты, инпуты, карта) держатся на bg-accent/text-accent/
        // border-accent/accent-accent. Удаление сломало бы их все разом —
        // оставляем алиас на CSS-переменную --accent (см. globals.css).
        accent: "var(--accent)",
      },
    },
  },
  plugins: [],
};

export default config;
