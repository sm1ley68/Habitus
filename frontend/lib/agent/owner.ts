// Формы кабинета продавца. Зеркало Go-DTO (handlers.OwnerListingDTO) и
// pydantic-схем (habitus/online/schema.py) — три стороны держатся синхронно.

export type OwnerListingStatus =
  | "draft"
  | "publishing"
  | "published"
  | "unpublished"
  | "failed";

export type OwnerListingOrigin = "cian" | "manual";

export type ImportVerdict = "new" | "claimable" | "already_yours";

export interface OwnerListing {
  id: string;
  external_id: string;
  origin: OwnerListingOrigin;
  status: OwnerListingStatus;
  verification: "unverified" | "verified";
  city: "msk" | "spb";
  price: number | null;
  area: number | null;
  kitchen_area: number | null;
  rooms: number | null;
  level: number | null;
  levels: number | null;
  address: string;
  /**
   * Всегда [lng, lat], WGS84 — как везде в проекте.
   * null у черновика, которому ещё не поставили точку на карте: подставлять
   * вместо неё [0, 0] запрещено — метка уехала бы в Гвинейский залив.
   */
  coordinates: [number, number] | null;
  window_orientation: string[];
  description: string;
  photos: string[];
  source_url: string;
  import_error: string;
  published_at: string | null;
  updated_at: string;
}

export interface SimilarListing {
  external_id: string;
  address: string;
  price: number | null;
  area: number | null;
}

export interface ImportPreview {
  verdict: ImportVerdict;
  draft: OwnerListing;
  /** Похожие объекты ортогональны вердикту: новое объявление тоже может их иметь. */
  similar: SimilarListing[];
  existing_id?: string;
}

/** Поля, которые фронт отправляет на создание и правку. */
export interface OwnerListingDraft {
  city?: "msk" | "spb";
  price?: number | null;
  area?: number | null;
  kitchen_area?: number | null;
  rooms?: number | null;
  level?: number | null;
  levels?: number | null;
  address?: string;
  coordinates?: [number, number];
  window_orientation?: string[];
  description?: string;
}

export const STATUS_LABEL: Record<OwnerListingStatus, string> = {
  draft: "Черновик",
  publishing: "Публикуется",
  published: "Опубликовано",
  unpublished: "Снято с публикации",
  failed: "Ошибка публикации",
};
