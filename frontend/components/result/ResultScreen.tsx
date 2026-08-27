"use client";
import EmptyResult from "@/components/chat/EmptyResult";
import { useSession } from "@/lib/store/session";
import SearchWorkspace from "./SearchWorkspace";

export default function ResultScreen() {
  const properties = useSession((s) => s.properties);
  const chatId = useSession((s) => s.chatId);
  if (properties.length === 0 && !chatId) {
    return (
      <div className="flex-1 grid place-items-center p-6">
        <EmptyResult />
      </div>
    );
  }
  return <SearchWorkspace />;
}
