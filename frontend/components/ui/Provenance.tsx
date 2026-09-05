import { PALETTE } from "@/lib/tokens";

export type ProvenanceKind = "measured" | "estimate" | "compromise";

const LABEL: Record<ProvenanceKind, string> = {
  measured: "замер",
  estimate: "оценка",
  compromise: "компромисс",
};

const COLOR: Record<ProvenanceKind, string> = {
  measured: PALETTE.evidence,
  estimate: PALETTE.estimate,
  compromise: PALETTE.compromise,
};

/** kind источника блока досье (schema.BlockSource.kind) → происхождение. */
export function provenanceOfSource(kind: string): ProvenanceKind {
  return kind === "proxy" ? "estimate" : "measured";
}

/** Плечо маршрута: estimate_kind заполнен — величина не замерена. */
export function provenanceOfLeg(leg: { estimate_kind?: string | null }): ProvenanceKind {
  return leg.estimate_kind ? "estimate" : "measured";
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
