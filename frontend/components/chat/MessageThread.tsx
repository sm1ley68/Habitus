"use client";
import SearchWaiting, { isThinking } from "./SearchWaiting";
import StageCaption from "./StageCaption";
import RelaxationCard from "./RelaxationCard";
import StreamingAnswer from "./StreamingAnswer";
import { useSession } from "@/lib/store/session";
export default function MessageThread() {
  const stage = useSession((s) => s.stage);
  const searchMessages = useSession((s) => s.searchMessages);
  const thinking = isThinking(stage);
  // SearchWaiting сам не видит историю чата — только стадию; последнюю
  // реплику человека ищем здесь и передаём пропом, как договорено в задаче.
  const request = [...searchMessages].reverse().find((m) => m.role === "user")?.text;
  return (
    <div className="flex flex-col gap-5 w-full">
      {stage === "relaxation" && <RelaxationCard />}
      {thinking ? <SearchWaiting request={request} /> : <StageCaption />}
      <StreamingAnswer />
    </div>
  );
}
