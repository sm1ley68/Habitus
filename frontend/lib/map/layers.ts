import type { LayerId } from "@/lib/agent/types";

/** Минимальный зум, с которого слой вообще рисует ТОЧКИ. Числа те же, что
 *  раньше жили в MapCanvas: вынесены сюда, потому что по ним теперь решается
 *  не только стиль, но и нужно ли вообще запрашивать слой у шлюза. */
export const MIN_POINT_ZOOM: Record<LayerId, number> = {
  metro: 10.5,
  schools: 12,
  parks: 12,
  bars: 14,
  // crime/noise/communal — линии и полигоны, порога точек у них нет.
  crime: 0,
  noise: 0,
  communal: 0,
};

export function minimumPointZoom(id: LayerId): number {
  return MIN_POINT_ZOOM[id] ?? 12;
}

/** Слои, состоящие ТОЛЬКО из точек. Ниже их порога зума карта не рисует
 *  ничего, поэтому и качать нечего: на центральном вьюпорте это 1423 бара,
 *  389 школ и 425 парков — больше мегабайта JSON, который парсится на главном
 *  потоке ради того, чтобы быть скрытым стилем.
 *
 *  metro сюда НЕ входит: кроме станций слой несёт линии метро/МЦК/МЦД, а они
 *  видны на любом зуме — пропуск запроса стёр бы их с карты. */
const POINT_ONLY_LAYERS: LayerId[] = ["schools", "bars", "parks"];

export function shouldFetchLayer(id: LayerId, zoom: number | null | undefined): boolean {
  // Зум ещё не известен (первый кадр, фолбэк-вьюпорт) — не решаем за карту.
  if (zoom == null || !Number.isFinite(zoom)) return true;
  if (!POINT_ONLY_LAYERS.includes(id)) return true;
  return zoom >= minimumPointZoom(id);
}

/** Границы зума, на которых стиль слоёв реально меняется (пороги видимости
 *  точек, подписи станций с 14.5 и отсечка полигонов crime на 15.5). */
const STYLE_ZOOM_BREAKS = [10.5, 12, 14, 14.5, 15.5];

/** Ключ стиля слоя на данном зуме. Пока ключ не изменился, `setStyle`
 *  вызывать не нужно — а это проход стайлера по КАЖДОЙ фиче слоя.
 *
 *  crime — исключение: его прозрачность (densityOpacity) непрерывно зависит от
 *  зума, и полосами градиент бы застыл. Для него ключ меняется каждые полшага
 *  зума: достаточно часто, чтобы заливка плыла, и достаточно редко, чтобы не
 *  перестиливать тысячи полигонов на каждое дробное движение колеса. */
export function zoomStyleKey(id: LayerId, zoom: number): string {
  if (id === "crime") return `crime:${Math.round(zoom * 2)}`;
  let band = 0;
  for (const breakpoint of STYLE_ZOOM_BREAKS) {
    if (zoom >= breakpoint) band += 1;
  }
  return `band:${band}`;
}
