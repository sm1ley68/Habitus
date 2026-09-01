/** Диф фич и маркеров вместо пересоздания слоя.
 *
 *  Раньше каждое движение карты сносило все `google.maps.Data` и все
 *  AdvancedMarkerElement и создавало их заново: на центральном вьюпорте это
 *  ~8800 фич и 2000 DOM-узлов на каждый шаг зума. Панорамирование почти всегда
 *  показывает те же самые объекты, поэтому дешевле сравнить ключи и тронуть
 *  только разницу. */

function firstPosition(geometry: GeoJSON.Geometry): number[] | null {
  if (geometry.type === "GeometryCollection") {
    for (const inner of geometry.geometries) {
      const found = firstPosition(inner);
      if (found) return found;
    }
    return null;
  }
  let node: unknown = geometry.coordinates;
  while (Array.isArray(node) && Array.isArray(node[0])) node = node[0];
  return Array.isArray(node) && typeof node[0] === "number" ? (node as number[]) : null;
}

function lastPosition(geometry: GeoJSON.Geometry): number[] | null {
  if (geometry.type === "GeometryCollection") {
    for (let i = geometry.geometries.length - 1; i >= 0; i -= 1) {
      const found = lastPosition(geometry.geometries[i]);
      if (found) return found;
    }
    return null;
  }
  let node: unknown = geometry.coordinates;
  while (Array.isArray(node) && Array.isArray(node[node.length - 1])) {
    node = node[node.length - 1];
  }
  return Array.isArray(node) && typeof node[0] === "number" ? (node as number[]) : null;
}

const round6 = (position: number[] | null) =>
  position ? position.slice(0, 2).map((n) => n.toFixed(6)).join(",") : "-";

/** Стабильный ключ фичи. Приоритет — явный id (у объявлений это external_id);
 *  дальше — геометрия плюс различающие свойства. Координаты округляются до
 *  шестого знака: столько же отдаёт PostGIS, и дрожание последнего бита не
 *  должно превращать ту же точку в «новую». */
export function featureKey(feature: GeoJSON.Feature): string {
  const props = (feature.properties ?? {}) as Record<string, unknown>;
  const explicit = feature.id ?? props.id;
  if (typeof explicit === "string" || typeof explicit === "number") return `id:${explicit}`;

  const geometry = feature.geometry;
  if (!geometry) return `void:${String(props.name ?? "")}`;
  const name = typeof props.name === "string" ? props.name : "";
  const ref = typeof props.ref === "string" ? props.ref : "";
  const system = typeof props.system === "string" ? props.system : "";
  const kind = typeof props.kind === "string" ? props.kind : "";
  if (geometry.type === "Point") {
    return `pt:${round6(firstPosition(geometry))}:${kind}:${name}`;
  }
  return [geometry.type, ref, system, name, round6(firstPosition(geometry)),
          round6(lastPosition(geometry))].join(":");
}

export function keyed(
  features: GeoJSON.Feature[],
  keyOf: (feature: GeoJSON.Feature) => string = featureKey,
): Map<string, GeoJSON.Feature> {
  const out = new Map<string, GeoJSON.Feature>();
  for (const feature of features) out.set(keyOf(feature), feature);
  return out;
}

/** Индекс фич по внешнему id. Фичи без id пропускаются: маркер объявления без
 *  идентификатора всё равно некуда открыть. */
export function keyById(features: GeoJSON.Feature[]): Map<string, GeoJSON.Feature> {
  const out = new Map<string, GeoJSON.Feature>();
  for (const feature of features) {
    const id = (feature.properties as Record<string, unknown> | null)?.id;
    if (typeof id === "string" && id) out.set(id, feature);
  }
  return out;
}

export function diffKeys(
  previous: Iterable<string>,
  next: Iterable<string>,
): { added: string[]; removed: string[] } {
  const before = new Set(previous);
  const after = new Set(next);
  const added: string[] = [];
  const removed: string[] = [];
  for (const key of after) if (!before.has(key)) added.push(key);
  for (const key of before) if (!after.has(key)) removed.push(key);
  return { added, removed };
}
