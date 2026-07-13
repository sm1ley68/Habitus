"use client";
import { useSession } from "@/lib/store/session";
export default function CitySwitch() {
  const city = useSession((s) => s.city);
  const setCity = useSession((s) => s.setCity);
  return (
    <div className="flex rounded-lg bg-zinc-100 p-1 text-[13px]">
      {(["spb", "msk"] as const).map((c) => (
        <button key={c} onClick={() => setCity(c)}
          className={`flex-1 rounded-md py-1.5 ${city === c ? "bg-white font-medium text-[#1c1d20]" : "text-zinc-500"}`}>
          {c === "spb" ? "Санкт-Петербург" : "Москва"}
        </button>
      ))}
    </div>
  );
}
