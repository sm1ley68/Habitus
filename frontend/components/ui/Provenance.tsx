import { PALETTE } from "@/lib/tokens";

export type ProvenanceKind = "measured" | "straight_line" | "model" | "compromise";

const LABEL: Record<ProvenanceKind, string> = {
  measured: "замер",
  straight_line: "по прямой",
  model: "модель",
  compromise: "компромисс",
};

const COLOR: Record<ProvenanceKind, string> = {
  measured: PALETTE.evidence,
  straight_line: PALETTE.estimate,
  model: PALETTE.estimate,
  compromise: PALETTE.compromise,
};

/** kind источника блока досье (schema.BlockSource.kind) → происхождение. */
export function provenanceOfSource(kind: string): ProvenanceKind {
  return kind === "proxy" ? "model" : "measured";
}

/** Плечо маршрута: estimate_kind конкретизирует вид оценки или неопределён — замер. */
export function provenanceOfLeg(leg: { estimate_kind?: string | null }): ProvenanceKind {
  switch (leg.estimate_kind) {
    case "straight_line":
      return "straight_line";
    case "model":
      return "model";
    default:
      return "measured";
  }
}

/** Цвет здесь — усиление, носитель смысла — слово: дальтоник и печать
 *  на чёрно-белом принтере обязаны читать то же самое. */
export default function Provenance({
  kind,
  className = "",
}: {
  kind: ProvenanceKind;
  className?: string;
}) {
  return (
    <span
      style={{ color: COLOR[kind] }}
      className={`text-[11px] uppercase tracking-[0.08em] ${className}`}
    >
      {LABEL[kind]}
    </span>
  );
}
