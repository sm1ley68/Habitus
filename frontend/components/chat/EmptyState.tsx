import ScenarioCards from "./ScenarioCards";

// Первый экран — не поисковая строка, а приглашение описать сценарий жизни.
// Тому, кто не знает, что писать, помогают три готовых примера: клик
// подставляет формулировку в композер, но не отправляет её — редактировать
// под себя ещё можно и нужно.
export default function EmptyState({ onPick }: { onPick: (text: string) => void }) {
  return (
    <div className="mx-auto w-full max-w-[720px]">
      <h1 className="font-display text-[34px] leading-[1.15] tracking-tight text-ink">
        Расскажите, как вы живёте — подберём, где жить
      </h1>
      <p className="mt-3 max-w-[52ch] text-ink-muted">
        Не фильтры, а сценарий: кто в семье, куда ездите каждый день,
        что важно и чем готовы поступиться.
      </p>
      <ScenarioCards onPick={onPick} />
    </div>
  );
}
