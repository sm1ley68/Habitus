// Карта человеческих подписей для ключей block.metrics — этот словарь приходит
// от ML свободной формы (habitus/online/schema.py), поэтому единственный
// источник правды по составу ключей — сама выдача, а не фиксированная схема.
// Показать пользователю сырое имя переменной (longest_leg_minutes) нельзя ни
// при каких условиях: неизвестный ключ не получает подпись и не рисуется вовсе.
const LABELS: Record<string, string> = {
  longest_leg_minutes: "Самая долгая поездка дня",
  bars_500m: "Баров и алкомаркетов в 500 м",
};

export function metricLabel(key: string): string | null {
  return LABELS[key] ?? null;
}
