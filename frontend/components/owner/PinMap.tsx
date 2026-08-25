"use client";
import maplibregl from "maplibre-gl";
import { useEffect, useRef } from "react";
import { CITY_CENTER } from "@/lib/map/constants";
import { useMaplibre } from "@/lib/map/useMaplibre";

/**
 * Карта с одной точкой. Точка вместо геокодера — сознательный выбор: она
 * точнее адреса строкой, срабатывает мгновенно и не тянет зависимость от
 * внешнего сервиса с лимитами. Адрес продавец пишет рядом — он идёт в текст
 * объявления, а не в определение места.
 *
 * Наружу координаты уходят строго как [lng, lat] — контракт всего проекта.
 */
export default function PinMap({
  value, onPick, city = "msk",
}: {
  value?: [number, number] | null;
  onPick: (coordinates: [number, number]) => void;
  city?: "msk" | "spb";
}) {
  const container = useRef<HTMLDivElement>(null);
  const { map, ready, missingKey } = useMaplibre(container);
  const marker = useRef<maplibregl.Marker | null>(null);
  const pick = useRef(onPick);
  pick.current = onPick;

  useEffect(() => {
    if (!map || !ready) return;
    const onClick = (e: maplibregl.MapMouseEvent) => {
      pick.current([e.lngLat.lng, e.lngLat.lat]);
    };
    map.on("click", onClick);
    return () => {
      map.off("click", onClick);
    };
  }, [map, ready]);

  useEffect(() => {
    if (!map || !ready) return;
    if (!value) {
      marker.current?.remove();
      marker.current = null;
      return;
    }
    if (!marker.current) {
      marker.current = new maplibregl.Marker({ color: "#6f7cc8", draggable: true })
        .setLngLat(value)
        .addTo(map);
      marker.current.on("dragend", () => {
        const { lng, lat } = marker.current!.getLngLat();
        pick.current([lng, lat]);
      });
    } else {
      marker.current.setLngLat(value);
    }
  }, [map, ready, value]);

  useEffect(
    () => () => {
      marker.current?.remove();
    },
    [],
  );

  if (missingKey) {
    return (
      <div className="grid h-72 place-items-center rounded-xl border border-zinc-200 bg-zinc-50 px-6 text-center text-sm text-zinc-500">
        Карта недоступна: не задан ключ картографического сервиса. Координаты можно
        будет поставить позже, из карточки объявления.
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div
        ref={container}
        className="h-72 w-full overflow-hidden rounded-xl border border-zinc-200"
      />
      <p className="text-xs text-zinc-400">
        {value
          ? `Точка: ${value[0].toFixed(5)}, ${value[1].toFixed(5)} — перетащите, если промахнулись`
          : `Нажмите на карту, чтобы поставить точку. Центр — ${city === "spb" ? "Санкт-Петербург" : "Москва"}, координаты ${CITY_CENTER[city][0].toFixed(2)}, ${CITY_CENTER[city][1].toFixed(2)}`}
      </p>
    </div>
  );
}
