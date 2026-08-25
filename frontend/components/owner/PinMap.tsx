"use client";
import { useEffect, useRef } from "react";
import { CITY_CENTER } from "@/lib/map/constants";
import { removeAdvancedMarker, toLatLng } from "@/lib/map/google";
import { useGoogleMap } from "@/lib/map/useGoogleMap";

/**
 * Карта с одной точкой. Точка вместо геокодера — сознательный выбор: она
 * точнее адреса строкой, срабатывает мгновенно и не тянет зависимость от
 * внешнего сервиса с лимитами. Адрес продавец пишет рядом — он идёт в текст
 * объявления, а не в определение места.
 *
 * Наружу координаты уходят строго как [lng, lat] — контракт всего проекта,
 * хотя Google внутри работает с {lat, lng}. Разворот держим в одном месте:
 * в toLatLng на входе и вручную на выходе из клика.
 */
export default function PinMap({
  value, onPick, city = "msk",
}: {
  value?: [number, number] | null;
  onPick: (coordinates: [number, number]) => void;
  city?: "msk" | "spb";
}) {
  const container = useRef<HTMLDivElement>(null);
  // cityAware отключён: камерой кабинета управляет поставленная точка, а не
  // город из сессии поиска — иначе карта улетала бы при смене города в шапке.
  const { map, ready, unavailable, missingKey } = useGoogleMap(container, {
    zoom: 15,
    cityAware: false,
  });
  const marker = useRef<google.maps.marker.AdvancedMarkerElement | null>(null);
  const pick = useRef(onPick);
  pick.current = onPick;
  const centered = useRef(false);

  useEffect(() => {
    if (!map || !ready) return;
    const listener = map.addListener("click", (event: google.maps.MapMouseEvent) => {
      if (event.latLng) pick.current([event.latLng.lng(), event.latLng.lat()]);
    });
    return () => listener.remove();
  }, [map, ready]);

  useEffect(() => {
    if (!map || !ready) return;

    if (!value) {
      removeAdvancedMarker(marker.current);
      marker.current = null;
      return;
    }

    if (!marker.current) {
      // Метка та же, что у объектов выдачи (.pin/.pin__dot в globals.css):
      // продавец видит свою квартиру ровно такой, какой её увидит покупатель.
      const dot = document.createElement("div");
      dot.className = "pin";
      const inner = document.createElement("span");
      inner.className = "pin__dot";
      dot.appendChild(inner);
      marker.current = new google.maps.marker.AdvancedMarkerElement({
        map,
        position: toLatLng(value),
        content: dot,
        gmpDraggable: true,
        title: "Точка объявления",
      });
      marker.current.addListener("dragend", (event: google.maps.MapMouseEvent) => {
        if (event.latLng) pick.current([event.latLng.lng(), event.latLng.lat()]);
      });
    } else {
      marker.current.position = toLatLng(value);
    }

    // Центрируем один раз, на первой точке: дальше продавец двигает карту сам,
    // и перехватывать у него камеру на каждый перетаск было бы вознёй.
    if (!centered.current) {
      map.panTo(toLatLng(value));
      centered.current = true;
    }
  }, [map, ready, value]);

  useEffect(
    () => () => {
      removeAdvancedMarker(marker.current);
      marker.current = null;
    },
    [],
  );

  if (unavailable) {
    return (
      <div className="grid h-72 place-items-center rounded-xl border border-zinc-200 bg-[#f6f7fb] px-6 text-center text-sm text-zinc-500">
        {missingKey
          ? "Добавьте Google Maps API key и Map ID в .env — без них точку на карте не поставить"
          : "Google Maps не удалось загрузить. Координаты можно поставить позже, из карточки объявления"}
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
