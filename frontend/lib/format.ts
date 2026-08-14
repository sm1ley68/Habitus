export function money(n: number | null | undefined): string {
  if (n === null || n === undefined) return "цена не указана";
  return Math.round((n / 1_000_000) * 10) / 10 + " млн ₽";
}
