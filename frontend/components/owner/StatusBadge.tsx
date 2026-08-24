import { Badge, type BadgeTone } from "@/components/ui";
import { STATUS_LABEL, type OwnerListingStatus } from "@/lib/agent/owner";

const TONE: Record<OwnerListingStatus, BadgeTone> = {
  published: "ok",
  failed: "danger",
  publishing: "neutral",
  draft: "warn",
  unpublished: "warn",
};

/** Цвет статуса — из палитры оценок паспорта; смысл несёт подпись. */
export const STATUS_RAIL: Record<OwnerListingStatus, string> = {
  published: "#2f8f5f",
  failed: "#b25e4a",
  publishing: "#6f7cc8",
  draft: "#d4d4d8",
  unpublished: "#b3822f",
};

export default function StatusBadge({ status }: { status: OwnerListingStatus }) {
  return <Badge tone={TONE[status]}>{STATUS_LABEL[status]}</Badge>;
}
