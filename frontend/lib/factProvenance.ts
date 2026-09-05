import type { ProvenanceKind } from "@/components/ui/Provenance";

/** Теги приходят строками с бэкенда, поэтому происхождение факта выводится
 *  по смыслу, а не по тексту тега. Пешие минуты и плотность баров — замер
 *  по данным (граф улиц, справочник заведений). Неизвестный тег метки не
 *  получает: выдумывать происхождение фронту не положено — это и есть
 *  правильный дефолт для тега, который сюда пока не заведён. */
export function factProvenance(tag: string): ProvenanceKind | null {
  if (/^\d+\s+минут/.test(tag)) return "measured";
  if (tag.startsWith("баров рядом")) return "measured";
  return null;
}
