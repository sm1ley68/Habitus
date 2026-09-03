"use client";
import { useSession } from "@/lib/store/session";
import { CITY_CLOSED_REASON, CITY_LABEL, isCitySearchable } from "@/lib/city";

// Порядок фиксированный: Москва первой, потому что она единственный
// наполненный город. Раньше первым стоял Петербург и читался как основной.
const CITIES = ["msk", "spb"] as const;

export default function CitySwitch() {
  const city = useSession((s) => s.city);
  const setCity = useSession((s) => s.setCity);
  return (
    <div className="flex rounded-lg bg-zinc-100 p-1 text-[13px]">
      {CITIES.map((c) => {
        const closed = CITY_CLOSED_REASON[c];
        return (
          <button
            key={c}
            disabled={!isCitySearchable(c)}
            onClick={() => setCity(c)}
            className={`flex-1 rounded-md py-1.5 disabled:cursor-not-allowed ${
              city === c ? "bg-white font-medium text-[#1c1d20]" : "text-zinc-500"
            } ${closed ? "text-zinc-400" : ""}`}
          >
            {CITY_LABEL[c]}
            {closed && (
              <span className="block text-[11px] font-normal text-zinc-400">{closed}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}
