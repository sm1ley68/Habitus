import { gradeTone } from "@/lib/grade";

export default function GradeBadge({ score }: { score: string }) {
  const tone = gradeTone(score);
  return (
    <span className="font-mono text-sm font-medium rounded-lg px-2.5 py-1"
      style={{ background: tone.bg, color: tone.color }}>
      {score}
    </span>
  );
}
