"use client";

import ZoneChip from "@/components/chat/ZoneChip";
import LayerToggles from "@/components/map/LayerToggles";
import MapCanvas from "@/components/map/MapCanvas";
import { useSession } from "@/lib/store/session";
import PropertyList from "./PropertyList";
import SearchWorkspaceChat from "./SearchWorkspaceChat";

export default function SearchWorkspace() {
  const areaLabel = useSession((state) => state.areaLabel);
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden p-4 lg:p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ZoneChip label={areaLabel} />
        <LayerToggles />
      </div>
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 xl:grid-cols-[minmax(270px,0.72fr)_minmax(420px,1.8fr)_minmax(280px,0.8fr)]">
        <PropertyList />
        <div className="order-1 min-h-[440px] xl:order-2 xl:min-h-0">
          <MapCanvas />
        </div>
        <SearchWorkspaceChat />
      </div>
    </div>
  );
}

