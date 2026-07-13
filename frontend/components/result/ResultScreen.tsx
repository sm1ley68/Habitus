"use client";
import PropertyList from "./PropertyList";
import EmptyResult from "@/components/chat/EmptyResult";
import MapCanvas from "@/components/map/MapCanvas";
import { useSession } from "@/lib/store/session";

export default function ResultScreen() {
  const properties = useSession((s) => s.properties);
  if (properties.length === 0) {
    return (
      <div className="flex-1 grid place-items-center p-6">
        <EmptyResult />
      </div>
    );
  }
  return (
    <div className="flex-1 grid grid-cols-1 lg:grid-cols-[1.2fr_1fr] gap-6 p-6 overflow-auto">
      <div className="min-h-[320px] order-first lg:order-none"><MapCanvas /></div>
      <PropertyList />
    </div>
  );
}
