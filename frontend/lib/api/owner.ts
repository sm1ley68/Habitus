import { API_BASE } from "./config";
import type {
  ImportPreview,
  OwnerListing,
  OwnerListingDraft,
} from "@/lib/agent/owner";

/** Ошибка с кодом бэка: экраны разводят по коду, а не по тексту. */
export class OwnerApiError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "OwnerApiError";
    this.code = code;
  }
}

async function failure(res: Response): Promise<OwnerApiError> {
  try {
    const body = await res.json();
    const code = body?.error?.code ?? "internal_error";
    const message = body?.error?.message ?? "Что-то пошло не так";
    return new OwnerApiError(code, message);
  } catch {
    return new OwnerApiError("internal_error", "Сервис недоступен. Попробуйте позже");
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, { credentials: "include", ...init });
  if (!res.ok) throw await failure(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function json(method: string, body?: unknown): RequestInit {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  };
}

export async function listOwnerListings(): Promise<OwnerListing[]> {
  const data = await request<{ listings: OwnerListing[] | null }>("/owner/listings");
  return data.listings ?? [];
}

export function getOwnerListing(id: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}`);
}

export function previewImport(url: string): Promise<ImportPreview> {
  return request<ImportPreview>("/owner/listings/import/preview", json("POST", { url }));
}

export function importListing(url: string): Promise<OwnerListing> {
  return request<OwnerListing>("/owner/listings/import", json("POST", { url }));
}

export function createListing(draft: OwnerListingDraft): Promise<OwnerListing> {
  return request<OwnerListing>("/owner/listings", json("POST", draft));
}

export function updateListing(id: string, draft: OwnerListingDraft): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}`, json("PATCH", draft));
}

export function publishListing(id: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}/publish`, json("POST"));
}

export function unpublishListing(id: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}/unpublish`, json("POST"));
}

export function deleteListing(id: string): Promise<void> {
  return request<void>(`/owner/listings/${id}`, { method: "DELETE" });
}

export function uploadPhotos(id: string, files: File[]): Promise<OwnerListing> {
  const form = new FormData();
  for (const file of files) form.append("photos", file);
  // Content-Type не задаём: границу multipart проставляет браузер.
  return request<OwnerListing>(`/owner/listings/${id}/photos`, { method: "POST", body: form });
}

export function deletePhoto(id: string, url: string): Promise<OwnerListing> {
  return request<OwnerListing>(`/owner/listings/${id}/photos`, json("DELETE", { url }));
}
