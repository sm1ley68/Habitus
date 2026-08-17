import type { LayerId } from "@/lib/agent/types";

// Значки внутри точек слоёв. Рисуются кодом на canvas и регистрируются как
// изображения карты — ни спрайта из стиля, ни внешних файлов: политика
// безопасности артефактов запрещает запросы на сторонние хосты, а стиль
// MapTiler не обязан содержать нужные нам иконки.
//
// Глиф описан списком примитивов, а не императивным рисованием: список —
// чистые данные, его можно проверить тестом без canvas, которого в jsdom нет.
// Система координат — квадрат 32×32 с центром в (16,16).
export type GlyphOp =
  | { op: "circle"; x: number; y: number; r: number }
  | { op: "rect"; x: number; y: number; w: number; h: number }
  | { op: "poly"; points: Array<[number, number]> }
  | { op: "text"; value: string; x: number; y: number; size: number };

export const ICON_PX = 32;      // сторона картинки
export const ICON_RATIO = 2;    // pixelRatio: 32px растра = 16 логических

// Силуэты намеренно простые: на карте значок занимает 10–14 логических
// пикселей, и любая деталь мельче штриха в 2px превращается в грязь.
export const POI_GLYPHS: Partial<Record<LayerId, GlyphOp[]>> = {
  // Дерево: хвойный треугольник и короткий широкий ствол. Круглая крона на
  // тонкой ножке проверку глазами не прошла — на 10 пикселях она читается
  // как лампочка. Треугольник — картографическая условность для зелени и
  // остаётся однозначным даже в мелком масштабе.
  parks: [
    { op: "poly", points: [[16, 4], [26, 19], [6, 19]] },
    { op: "rect", x: 13.5, y: 18, w: 5, h: 8 },
  ],
  // Книга: разворот двумя страницами с корешком посередине.
  schools: [
    { op: "poly", points: [[5, 8], [15, 10], [15, 24], [5, 22]] },
    { op: "poly", points: [[17, 10], [27, 8], [27, 22], [17, 24]] },
  ],
  // Бокал: треугольная чаша, ножка, основание.
  bars: [
    { op: "poly", points: [[7, 7], [25, 7], [16, 18]] },
    { op: "rect", x: 15, y: 18, w: 2, h: 6 },
    { op: "rect", x: 10, y: 24, w: 12, h: 2.5 },
  ],
  // Метро: буква М. Узнаваемее любого силуэта поезда.
  metro: [{ op: "text", value: "М", x: 16, y: 16, size: 21 }],
};

export function poiIconImageId(id: LayerId): string {
  return `habitus-poi-${id}`;
}

// Минимальная часть CanvasRenderingContext2D, которой мы пользуемся. Свой тип
// нужен, чтобы тест мог подсунуть запоминающую заглушку вместо настоящего
// контекста: в jsdom canvas не реализован и getContext возвращает null.
export interface GlyphContext {
  fillStyle: string;
  font: string;
  textAlign: string;
  textBaseline: string;
  beginPath(): void;
  closePath(): void;
  moveTo(x: number, y: number): void;
  lineTo(x: number, y: number): void;
  arc(x: number, y: number, r: number, a0: number, a1: number): void;
  rect(x: number, y: number, w: number, h: number): void;
  fill(): void;
  fillText(text: string, x: number, y: number): void;
}

export function drawGlyph(ctx: GlyphContext, ops: GlyphOp[]): void {
  ctx.fillStyle = "#ffffff";
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  for (const op of ops) {
    if (op.op === "text") {
      // Системный гротеск: своих шрифтов у нас нет, а грузить их запрещено.
      ctx.font = `700 ${op.size}px -apple-system, "Helvetica Neue", Arial, sans-serif`;
      ctx.fillText(op.value, op.x, op.y);
      continue;
    }
    ctx.beginPath();
    if (op.op === "circle") ctx.arc(op.x, op.y, op.r, 0, Math.PI * 2);
    else if (op.op === "rect") ctx.rect(op.x, op.y, op.w, op.h);
    else {
      op.points.forEach(([x, y], i) => (i ? ctx.lineTo(x, y) : ctx.moveTo(x, y)));
      ctx.closePath();
    }
    ctx.fill();
  }
}

// Растрирует глиф в ImageData. Возвращает null, когда canvas недоступен
// (серверный рендер, jsdom) — вызывающая сторона обязана это пережить:
// карта без значков остаётся рабочей, точки просто останутся кружками.
export function renderGlyph(id: LayerId): ImageData | null {
  const ops = POI_GLYPHS[id];
  if (!ops || typeof document === "undefined") return null;
  const canvas = document.createElement("canvas");
  canvas.width = ICON_PX;
  canvas.height = ICON_PX;
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  drawGlyph(ctx as unknown as GlyphContext, ops);
  return ctx.getImageData(0, 0, ICON_PX, ICON_PX);
}

interface ImageHost {
  hasImage(id: string): boolean;
  addImage(id: string, image: ImageData, options?: { pixelRatio?: number }): void;
}

/** Регистрирует значки один раз на карту. Повторный вызов безвреден. */
export function registerPoiIcons(map: ImageHost): void {
  (Object.keys(POI_GLYPHS) as LayerId[]).forEach((id) => {
    const imageId = poiIconImageId(id);
    if (map.hasImage(imageId)) return;
    const data = renderGlyph(id);
    if (data) map.addImage(imageId, data, { pixelRatio: ICON_RATIO });
  });
}
