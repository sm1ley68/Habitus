// Data-seam configuration. Flipping these two env vars is the only change a
// backend dev makes to go live — no UI/component edits.
//
//   NEXT_PUBLIC_USE_MOCK=false   → use the real fetch/SSE implementation
//   NEXT_PUBLIC_API_BASE=https://api.example.com/api/v1  → point at the backend
//
// Default (unset) keeps the app on mock data so it runs offline.
export const USE_MOCK = process.env.NEXT_PUBLIC_USE_MOCK !== "false";
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "/api/v1";
