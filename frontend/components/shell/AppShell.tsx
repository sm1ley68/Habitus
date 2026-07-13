"use client";
import { useSession } from "@/lib/store/session";
import EdgeGlow from "@/components/fx/EdgeGlow";
import LeftRail from "./LeftRail";
import HistorySidebar from "./HistorySidebar";
import ChatScreen from "@/components/chat/ChatScreen";
import ResultScreen from "@/components/result/ResultScreen";
import MapScreen from "@/components/map/MapScreen";
import PassportScreen from "@/components/passport/PassportScreen";

export default function AppShell() {
  const screen = useSession((s) => s.screen);
  return (
    <div data-testid="app-shell" className="relative h-[100dvh] flex bg-white text-[#1c1d20] overflow-hidden">
      <EdgeGlow />
      <LeftRail />
      <HistorySidebar />
      <main className="relative z-[2] flex-1 flex min-h-0 min-w-0 pb-16 md:pb-0">
        {screen === "chat" && <ChatScreen />}
        {screen === "result" && <ResultScreen />}
        {screen === "map" && <MapScreen />}
        {screen === "passport" && <PassportScreen />}
      </main>
    </div>
  );
}
