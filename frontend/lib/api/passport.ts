import type { ObjectPassport, LifestyleAnalysis, Property } from "@/lib/agent/types";
import { PROPERTIES, DOSSIER, LIFESTYLE_BLOCKS } from "@/lib/data/mock";
import { USE_MOCK, API_BASE } from "./config";

// The single data-seam for the object passport. Components call this instead of
// importing mock modules directly, so swapping to the real backend is a config
// flip (NEXT_PUBLIC_USE_MOCK=false) with no UI changes. The returned shape is the
// contract's ObjectPassport (§4 + Н.1–Н.3) verbatim — zero mapping downstream.

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

// Build a passport from local mock data, mirroring the backend response shape.
function mockPassport(property: Property): ObjectPassport {
  const lifestyle_analysis: LifestyleAnalysis = {
    match_score: property.match_score,
    summary:
      // No dedicated mock summary field — derive a sensible one from the verdict.
      `${DOSSIER.verdict.headline}. Проверено ${DOSSIER.verdict.layers_checked} слоёв города.`,
    verdict: DOSSIER.verdict,
    brief: DOSSIER.brief,
    blocks: LIFESTYLE_BLOCKS,
    compromises: DOSSIER.compromises,
    relaxation: DOSSIER.relaxation,
    zone_rationale: DOSSIER.zone_rationale,
  };

  return {
    id: property.id,
    name: property.name,
    // Synthesized from the property until the backend provides a real address.
    address: `Санкт-Петербург, ${property.name}`,
    price: property.price_from,
    rooms: property.rooms,
    area_sqm: property.area_sqm,
    floor: property.floor,
    images: [property.cover_image],
    coordinates: property.coordinates,
    lifestyle_analysis,
  };
}

export async function getObjectPassport(
  objectId: string,
  chatId?: string,
): Promise<ObjectPassport> {
  if (USE_MOCK) {
    // Small delay so the loading/skeleton state is real, not a flash.
    await wait(250);
    const property =
      PROPERTIES.find((p) => p.id === objectId) ?? PROPERTIES[0];
    return mockPassport(property);
  }

  const res = await fetch(
    `${API_BASE}/objects/${objectId}?chat_id=${chatId ?? ""}`,
    { credentials: "include" },
  );
  if (!res.ok) {
    throw new Error(`getObjectPassport failed: ${res.status}`);
  }
  return (await res.json()) as ObjectPassport;
}
