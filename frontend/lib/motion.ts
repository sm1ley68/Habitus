// Single source of truth for motion. framer-motion consumes SPRING/EASE/DUR;
// CSS consumes the mirrored vars in globals.css. Change feel here, everywhere.
export const DUR = {
  instant: 0.08,
  fast: 0.14,
  base: 0.24,
  slow: 0.42,
  cinematic: 1.2,
} as const;

export const EASE = {
  standard: [0.4, 0, 0.2, 1],
  emphasizedDecelerate: [0.05, 0.7, 0.1, 1],
  emphasizedAccelerate: [0.3, 0, 0.8, 0.15],
  glow: [0.645, 0.045, 0.355, 1],
} as const;

export const SPRING = {
  snappy: { type: "spring", stiffness: 420, damping: 32 },
  soft: { type: "spring", stiffness: 140, damping: 20 },
  gentle: { type: "spring", stiffness: 90, damping: 22 },
} as const;
