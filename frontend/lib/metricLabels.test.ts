import { metricLabel } from "./metricLabels";

// metrics приходит от ML свободным словарём (habitus/online/schema.py), поэтому
// ключ может быть любым — в том числе таким, для которого подписи ещё нет.
// Показывать пользователю сырое имя переменной запрещено: лучше не показать
// ничего, чем показать longest_leg_minutes.
it("известный ключ получает человеческую подпись", () => {
  expect(metricLabel("longest_leg_minutes")).toBe("Самая долгая поездка дня");
  expect(metricLabel("bars_500m")).toBe("Баров и алкомаркетов в 500 м");
});

it("неизвестный ключ не показывается вовсе", () => {
  expect(metricLabel("some_new_metric")).toBeNull();
});
