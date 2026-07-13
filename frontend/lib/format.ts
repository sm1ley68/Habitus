export function money(n: number): string {
  return Math.round((n / 1_000_000) * 10) / 10 + " млн ₽";
}
