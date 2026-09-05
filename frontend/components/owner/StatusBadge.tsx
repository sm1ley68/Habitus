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
  // publishing — процесс идёт прямо сейчас, ни хорошо, ни плохо: остальным
  // статусам тут не быть (зелёный/терракотовый/золото уже заняты смыслом
  // готово/ошибка/предупреждение). Раньше был индиго дефолтного комплекта;
  // берём приглушённый стальной синий — он не спорит ни с одним из
  // существующих статусов и не пересекается с палитрой происхождения факта.
  publishing: "#4F7A9E",
  draft: "#d4d4d8",
  unpublished: "#b3822f",
};

export default function StatusBadge({ status }: { status: OwnerListingStatus }) {
  return <Badge tone={TONE[status]}>{STATUS_LABEL[status]}</Badge>;
}
