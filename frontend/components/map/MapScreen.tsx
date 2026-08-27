"use client";
import MapCanvas from "./MapCanvas";
import LayerToggles from "./LayerToggles";
import SearchWorkspace from "@/components/result/SearchWorkspace";
import { useSession } from "@/lib/store/session";

export default function MapScreen() {
  const hasSearchContext = useSession((state) => Boolean(state.chatId));
  if (hasSearchContext) return <SearchWorkspace />;
  return (
    <div className="flex-1 flex flex-col gap-4 p-6">
      <LayerToggles />
      <div className="flex-1 min-h-[400px]"><MapCanvas /></div>
    </div>
  );
}
