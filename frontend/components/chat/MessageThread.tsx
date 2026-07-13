"use client";
import GeoThinkingCanvas, { isThinking } from "./GeoThinkingCanvas";
import StageCaption from "./StageCaption";
import RelaxationCard from "./RelaxationCard";
import StreamingAnswer from "./StreamingAnswer";
import { useSession } from "@/lib/store/session";
export default function MessageThread() {
  const stage = useSession((s) => s.stage);
  const thinking = isThinking(stage);
  return (
    <div className="flex flex-col gap-5 w-full">
      {stage === "relaxation" && <RelaxationCard />}
      {thinking ? <GeoThinkingCanvas /> : <StageCaption />}
      <StreamingAnswer />
    </div>
  );
}
