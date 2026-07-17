import type { City } from "@/lib/agent/types";

// Центры городов для стартовой камеры, пока поиск не прислал зону.
// Данные пайплайна сейчас московские (CITY_REGION_CODE), см. CLAUDE.md.
export const CITY_CENTER: Record<City, [number, number]> = {
  msk: [37.6173, 55.7558],
  spb: [30.3351, 59.9343],
};
