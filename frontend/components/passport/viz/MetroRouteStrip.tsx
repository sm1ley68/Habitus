"use client";
import { Fragment } from "react";
import type { MetroRide, MetroSegment, MetroSystem } from "@/lib/agent/types";
import { pluralRu } from "@/lib/format";

// Подпись системы словом. Цвет линии подсказывает, текст утверждает — тем же
// правилом, что уже соблюдает FamilyDayGraph: цвет никогда не единственный
// носитель смысла.
export const SYSTEM_LABEL: Record<MetroSystem, string> = {
  subway: "метро",
  mck: "МЦК",
  mcd: "МЦД",
};

// Запасной цвет для линии, у которой в OSM не проставлен colour (null или "").
export const FALLBACK_COLOUR = "#71717a";

// Суффикс с номером линии — только когда он что-то добавляет к слову системы.
// МЦК — одна линия, line_ref обычно совпадает с кодом системы («MCK»):
// «МЦК-MCK» был бы бессмысленным дублем. У МЦД несколько диаметров
// (D1, D2, D3…) — там суффикс обязателен, иначе неотличимо, какой из них.
function lineSuffix(seg: MetroSegment): string {
  if (seg.system === "subway") return "";
  if (seg.line_ref.toUpperCase() === seg.system.toUpperCase()) return "";
  return `-${seg.line_ref}`;
}

function Dot({ colour, testId }: { colour: string | null; testId: string }) {
  return (
    <span
      aria-hidden
      data-testid={testId}
      className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
      style={{ background: colour || FALLBACK_COLOUR }}
    />
  );
}

function Walk({
  minutes,
  note,
  testId,
  title,
}: {
  minutes: number;
  note?: string;
  testId: string;
  title: string;
}) {
  return (
    <li
      data-testid={testId}
      title={title}
      className="flex items-center gap-2 text-xs text-zinc-600"
    >
      <span
        aria-hidden
        className="inline-block h-2.5 w-2.5 shrink-0 rounded-full border border-dashed border-zinc-400"
      />
      <span>
        {minutes} мин пешком{note ? ` ${note}` : ""}
      </span>
    </li>
  );
}

export default function MetroRouteStrip({ ride }: { ride: MetroRide }) {
  // R84c: транспортов может прийти больше, чем сегментов, из malformed
  // ride — `ride.transfers[i]` в цикле ниже читает только i < segments.length,
  // и «хвост» пересадок молча пропал бы без единого сигнала. Не роняем
  // рендер (это данные с бэка, не баг фронта), но делаем потерю видимой.
  if (ride.transfers.length > ride.segments.length) {
    console.warn(
      `MetroRouteStrip: в поездке ${ride.transfers.length} пересадок, но только ` +
        `${ride.segments.length} сегментов — «хвост» пересадок не отрисован`,
    );
  }

  return (
    <div className="rounded-lg border border-zinc-100 p-3">
      <ol aria-label="маршрут поездки" className="flex flex-col gap-2">
        {/* Оценка по прямой до станции/платформы — НЕ измеренный по улицам
            walk_min_metro с карточки объявления (тот считает ORS и только по
            подземке). Обе цифры валидны, но про разное — заголовок называет,
            какая это, чтобы их не спутали и одна не перекрыла другую. */}
        <Walk
          minutes={ride.walk_from_home_min}
          note="до станции"
          testId="walk-from-home"
          title="Оценка по прямой до станции — не измеренное по улицам время walk_min_metro с карточки объявления"
        />

        {ride.segments.map((seg, i) => (
          <Fragment key={`${seg.line_ref}-${i}`}>
            <li
              data-testid={`segment-${i}`}
              className="flex items-start gap-2 text-xs"
            >
              <span className="mt-1.5">
                <Dot colour={seg.colour} testId={`segment-${i}-dot`} />
              </span>
              <span className="text-[#1c1d20]">
                <span className="font-medium">{seg.from_station}</span>
                {" → "}
                <span className="font-medium">{seg.to_station}</span>
                <span className="text-zinc-500">
                  {" · "}
                  {SYSTEM_LABEL[seg.system]}
                  {lineSuffix(seg)}
                  {" · "}
                  {seg.stops} {pluralRu(seg.stops, "станция", "станции", "станций")}
                  {" · "}
                  {seg.minutes} мин
                  {/* Метка сегмента идёт от seg.estimated, а не от
                      ride.estimated: поездка может быть estimated из-за
                      оценённой пешей ноги, а сами рельсовые отрезки — точными
                      данными из курируемого файла. Обратное тоже возможно. */}
                  {seg.estimated ? " · оценка" : ""}
                </span>
              </span>
            </li>

            {ride.transfers[i] ? (
              <li
                data-testid={`transfer-${i}`}
                className="flex items-center gap-2 text-xs text-zinc-600"
              >
                <span
                  aria-hidden
                  className="inline-block h-2.5 w-2.5 shrink-0 rotate-45 border border-zinc-400"
                />
                <span>
                  переход {ride.transfers[i].from_station} → {ride.transfers[i].to_station}
                  {" · "}
                  {ride.transfers[i].minutes} мин
                  {ride.transfers[i].outdoor ? " · улицей" : ""}
                  {ride.transfers[i].estimated ? " · оценка" : ""}
                </span>
              </li>
            ) : null}
          </Fragment>
        ))}

        <Walk
          minutes={ride.walk_to_dest_min}
          note="от станции"
          testId="walk-to-dest"
          title="Оценка по прямой от станции — не измеренное по улицам время walk_min_metro с карточки объявления"
        />

        {/* wait_min — НЕ независимый замер ожидания посадки (см. докстринг
            MetroRide.wait_min в habitus/online/schema.py): это остаток
            округления, total_minutes минус уже показанные части выше, каждая
            из которых округлена независимо. Подписываем его так и только
            так — «время ожидания поезда» здесь было бы неправдой.
            R82: ride.wait_min === 0 достижим (metro_route.py клэмпит
            max(0, …)), но это ложь для реального рельсового маршрута
            (headway всегда > 0) — рисовать «≈0 мин» здесь был бы тот же
            запрещённый синтетический ноль, что и везде в проекте. Строка
            пропадает целиком, а не показывает 0. */}
        {ride.wait_min > 0 ? (
          <li data-testid="wait-note" className="pl-[1.125rem] text-xs text-zinc-400">
            ещё ≈{ride.wait_min} мин — остаток округления по дороге, не самостоятельный замер ожидания посадки
          </li>
        ) : null}
      </ol>

      <p
        data-testid="total"
        className="mt-3 border-t border-zinc-100 pt-2 text-xs font-medium text-[#1c1d20]"
      >
        {/* Итог берётся из контракта, а НЕ складывается из частей заново:
            округления каждого шага разошлись бы с числом, по которому
            фильтровался поиск (инвариант описан в MetroRide.wait_min). */}
        {ride.total_minutes} мин от двери до двери
        {ride.estimated ? (
          <span className="ml-2 font-normal text-zinc-500">
            часть величин маршрута — оценка, а не замер
          </span>
        ) : null}
      </p>
    </div>
  );
}
