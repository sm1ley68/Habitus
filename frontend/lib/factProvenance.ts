import type { ProvenanceKind } from "@/components/ui/Provenance";

/** Теги приходят строками с бэкенда, поэтому происхождение факта выводится
 *  по смыслу, а не по тексту тега. Пешие минуты и плотность баров — замер
 *  по данным (граф улиц, справочник заведений); шум — величина, посчитанная
 *  моделью по типам дорог и барному прокси, а не измеренная напрямую, поэтому
 *  это "model", а не "estimate" (такого значения в ProvenanceKind больше нет —
 *  оно разведено на straight_line/model). Неизвестный тег метки не получает:
 *  выдумывать происхождение фронту не положено. */
export function factProvenance(tag: string): ProvenanceKind | null {
  if (/^\d+\s+минут/.test(tag)) return "measured";
  if (tag.startsWith("баров рядом")) return "measured";
  if (tag.includes("шум")) return "model";
  return null;
}
