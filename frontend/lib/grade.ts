export function gradeTone(score: string): { bg: string; color: string } {
  const letter = score[0];
  if (letter === "A") return { bg: "#e9f5ee", color: "#2f8f5f" };
  if (letter === "B") return { bg: "#f8f0e0", color: "#b3822f" };
  return { bg: "#f6ece9", color: "#b25e4a" };
}
