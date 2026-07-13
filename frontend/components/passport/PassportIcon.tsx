import type { LifestyleIcon } from "@/lib/agent/types";

const PATHS: Record<LifestyleIcon, string> = {
  school: "M3 9l9-5 9 5-9 5-9-5zm0 0v6m6 1v-4",
  users: "M16 19v-2a4 4 0 00-4-4H6a4 4 0 00-4 4v2M9 7a3 3 0 100 6 3 3 0 000-6zm11 12v-2a4 4 0 00-3-3.87",
  sun: "M12 4V2m0 20v-2m8-8h2M2 12h2m13.66-5.66l1.42-1.42M4.92 19.08l1.42-1.42m0-11.32L4.92 4.92m14.16 14.16l-1.42-1.42M12 8a4 4 0 100 8 4 4 0 000-8z",
  volume: "M11 5L6 9H2v6h4l5 4V5zm4.5 3a5 5 0 010 8",
  leaf: "M11 20A7 7 0 014 13c0-5 5-9 12-9 0 7-4 12-9 12z",
  hospital: "M4 21V7l8-4 8 4v14M9 21v-6h6v6M12 8v4m-2-2h4",
  route: "M6 19a3 3 0 100-6 3 3 0 000 6zm12-8a3 3 0 100-6 3 3 0 000 6zm-9 5h6a3 3 0 003-3V9",
};

export default function PassportIcon({ name }: { name: LifestyleIcon }) {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true"
      stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d={PATHS[name]} />
    </svg>
  );
}
