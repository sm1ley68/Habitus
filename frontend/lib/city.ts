// Грубые bbox городов — зеркало habitus/clean/normalize.py:CITY_BBOX.
// Фронт по ним определяет город точки, а не спрашивает его у продавца:
// человек, поставивший точку на карте, уже сказал, где квартира.
export const CITY_BBOX: Record<"msk" | "spb", [number, number, number, number]> = {
  msk: [37.3, 55.48, 37.95, 55.95],
  spb: [29.6, 59.7, 30.7, 60.2],
};

/**
 * Город по координатам [lng, lat]. Точка вне обоих городов — не ошибка фронта:
 * последнее слово за бэком, который проверит bbox ещё раз и откажет внятно.
 * Здесь возвращаем msk как рабочее умолчание пайплайна.
 */
export function cityByCoordinates([lng, lat]: [number, number]): "msk" | "spb" {
  for (const [city, [lngMin, latMin, lngMax, latMax]] of Object.entries(CITY_BBOX)) {
    if (lng >= lngMin && lng <= lngMax && lat >= latMin && lat <= latMax) {
      return city as "msk" | "spb";
    }
  }
  return "msk";
}

export const CITY_LABEL: Record<"msk" | "spb", string> = {
  msk: "Москва",
  spb: "Санкт-Петербург",
};

/**
 * Почему город закрыт для поиска; null — город наполнен и доступен.
 *
 * Признак ровно один, а не пара «флаг + текст»: два поля со временем
 * разъезжаются, а причина без флага невозможна по построению.
 *
 * Санкт-Петербург закрыт, потому что по нему нет ни слоёв urban_evidence, ни
 * admin-зон, ни платформ метро (см. habitus/geo/metro_access.py). Открытый
 * переключатель обещал бы рабочий город, а отдавал бы пустую выдачу — это
 * ровно тот случай, который продукт обязан называть вслух, а не маскировать.
 * Enum'ы `msk | spb` на всех трёх сторонах при этом остаются нетронутыми:
 * закрываем вход, а не контракт, — данные появятся.
 */
export const CITY_CLOSED_REASON: Record<"msk" | "spb", string | null> = {
  msk: null,
  spb: "данные готовим",
};

export function isCitySearchable(city: "msk" | "spb"): boolean {
  return CITY_CLOSED_REASON[city] === null;
}
