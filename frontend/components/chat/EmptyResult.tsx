export default function EmptyResult() {
  return (
    <div className="text-center max-w-md mx-auto">
      <h2 className="text-xl font-medium">В этой зоне пока нет точных совпадений</h2>
      <p className="mt-2 text-zinc-500">Могу немного ослабить критерии — например, увеличить время до школы до 20 минут.</p>
      <button className="mt-4 rounded-full bg-accent text-white px-5 py-2.5 text-sm active:scale-95">Ослабить критерии</button>
    </div>
  );
}
