// Три готовых сценария для человека, который не знает, с чего начать
// формулировку. Каждая карточка подставляет целую фразу нужного уровня
// подробности — а не ключевое слово — чтобы было видно, как вообще стоит
// описывать свою жизнь агенту.
const SCENARIOS = [
  {
    title: "Ребёнок ходит в школу сам",
    text: "Двушка до 25 млн, ребёнку в школу пешком без крупных дорог, мне на работу в Сити на метро",
  },
  {
    title: "Два офиса на разных концах",
    text: "Ищем компромисс между офисом в Сколково и офисом в Сити, двушка до 50 млн",
  },
  {
    title: "Работаю из дома, нужна тишина",
    text: "Тихая квартира с парком рядом, работаю удалённо, супруга ездит к МГУ",
  },
];

export default function ScenarioCards({ onPick }: { onPick: (text: string) => void }) {
  return (
    <div className="mt-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
      {SCENARIOS.map((s) => (
        <button
          key={s.title}
          type="button"
          onClick={() => onPick(s.text)}
          className="rounded-lg border border-black/[0.08] bg-white px-4 py-3.5 text-left transition-colors duration-200 ease-out hover:border-black/[0.14] hover:bg-paper focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <div className="text-sm font-medium text-ink">{s.title}</div>
          <div className="mt-1 text-[13px] leading-snug text-ink-faint">{s.text}</div>
        </button>
      ))}
    </div>
  );
}
