"use client";
import { useMemo, useState } from "react";
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
  // Клик по карточке сценария подставляет формулировку в композер, но не
  // отправляет её — человек должен успеть отредактировать текст под себя.
  const [draft, setDraft] = useState<string | undefined>(undefined);

  // Idle: heading + composer centered together над спокойной бумажной
  // подложкой. Индиговый градиент убран — в проекте индиго нигде не остаётся.
  if (idle) {
    return (
      <div className="relative flex-1 flex flex-col items-center justify-center px-4">
        <div className="relative flex w-full flex-col items-center gap-10">
          <EmptyState onPick={setDraft} />
          <Composer onSubmit={send} draft={draft} />
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
