export function money(n: number | null | undefined): string {
  if (n === null || n === undefined) return "цена не указана";
  return Math.round((n / 1_000_000) * 10) / 10 + " млн ₽";
}

/**
 * Русское согласование числительного со словом: 1 станция, 2 станции,
 * 5 станций, 11 станций, 21 станция — стандартное правило (last two digits
 * 11-14 → форма «многих», иначе по последней цифре).
 */
export function pluralRu(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return many;
  if (mod10 === 1) return one;
  if (mod10 >= 2 && mod10 <= 4) return few;
  return many;
}
