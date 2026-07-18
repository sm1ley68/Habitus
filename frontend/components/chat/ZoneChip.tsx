export default function ZoneChip({ label }: { label: string | null }) {
  if (!label) return null;
  return (
    <span className="inline-flex items-center gap-1 self-start rounded-full border border-zinc-200 bg-white px-3 py-1 text-xs font-medium text-zinc-700 shadow-sm">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
        <path d="M12 21s-6-5.686-6-10a6 6 0 1112 0c0 4.314-6 10-6 10z" />
        <circle cx="12" cy="11" r="2" />
      </svg>
      {label}
    </span>
  );
}
