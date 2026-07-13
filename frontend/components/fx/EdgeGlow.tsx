"use client";
import { useEffect, useRef } from "react";
import { useSession } from "@/lib/store/session";
import { STAGE_GLOW } from "@/lib/agent/stageVisuals";

const AURA_GRADIENTS =
  "radial-gradient(ellipse 55% 38% at 50% -8%, var(--glow-color), transparent 60%)," +
  "radial-gradient(ellipse 55% 38% at 50% 108%, var(--glow-color), transparent 60%)," +
  "radial-gradient(ellipse 36% 60% at -8% 50%, var(--glow-color), transparent 60%)," +
  "radial-gradient(ellipse 36% 60% at 108% 50%, var(--glow-color), transparent 60%)";

export default function EdgeGlow() {
  const stage = useSession((s) => s.stage);
  const ref = useRef<HTMLDivElement>(null);

  // Drive the typed CSS custom properties; the browser interpolates them across
  // the transition declared below — color crossfades instead of snapping.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const { color, opacity } = STAGE_GLOW[stage];
    el.dataset.targetOpacity = String(opacity);
    el.dataset.stage = stage;
    el.style.setProperty("--glow-color", color);
    el.style.setProperty("--glow-opacity", String(opacity));
  }, [stage]);

  return (
    <div
      ref={ref}
      data-testid="edge-glow"
      aria-hidden="true"
      className="pointer-events-none fixed inset-0 z-[1]"
      style={{
        opacity: "var(--glow-opacity)",
        // Stage on-screen morph -> ease-in-out; color + master opacity together.
        transition:
          "opacity 1200ms var(--ease-glow), --glow-color 1200ms var(--ease-glow)",
      }}
    >
      {/* Inner aura: holds the gradients, blur, and the perpetual "breathing".
          Kept separate so the living pulse never fights the stage opacity. */}
      <div
        className="absolute inset-0"
        style={{
          background: AURA_GRADIENTS,
          filter: "blur(50px)",
          transformOrigin: "center",
          willChange: "transform",
          animation: "glowBreathe 7s ease-in-out infinite",
        }}
      />
    </div>
  );
}
