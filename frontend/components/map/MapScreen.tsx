"use client";
import MapCanvas from "./MapCanvas";
import LayerToggles from "./LayerToggles";

export default function MapScreen() {
  return (
    <div className="flex-1 flex flex-col gap-4 p-6">
      <LayerToggles />
      <div className="flex-1 min-h-[400px]"><MapCanvas /></div>
    </div>
  );
}
