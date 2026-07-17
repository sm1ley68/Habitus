"use client";
import { useMemo } from "react";
import Composer from "./Composer";
import EmptyState from "./EmptyState";
import ErrorState from "./ErrorState";
import MessageThread from "./MessageThread";
import { useSession } from "@/lib/store/session";
import { createSearchClient } from "@/lib/api/searchStream";

export default function ChatScreen() {
  const stage = useSession((s) => s.stage);
  const start = useSession((s) => s.startQuery);
  const client = useMemo(() => createSearchClient(), []);
  const idle = stage === "idle";
  const error = stage === "error";
  const send = (text: string) => start(client, text);

  // Idle (Gemini-like): heading + composer centered together over a soft ambient.
  if (idle) {
    return (
      <div className="relative flex-1 flex flex-col items-center justify-center px-4">
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
          style={{
            background:
              "radial-gradient(52% 40% at 50% 52%, rgba(124,140,255,0.09), transparent 70%)",
          }}
        />
        <div className="relative flex w-full flex-col items-center gap-10">
          <EmptyState />
          <Composer onSubmit={send} />
        </div>
      </div>
    );
  }

  // Conversation / thinking / error: thread grows, composer docks to the bottom.
  return (
    <div className="flex-1 flex flex-col px-4 py-10">
      <div className="flex-1 grid place-items-center w-full">
        {error ? <ErrorState /> : <MessageThread />}
      </div>
      <Composer onSubmit={send} />
    </div>
  );
}
