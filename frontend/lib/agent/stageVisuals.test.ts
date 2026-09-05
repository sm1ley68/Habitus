import { STAGE_GLOW } from "./stageVisuals";
import { PALETTE } from "@/lib/tokens";

test("idle is invisible, done is invisible", () => {
  expect(STAGE_GLOW.idle.opacity).toBe(0);
  expect(STAGE_GLOW.done.opacity).toBe(0);
});

// Свечение — акцент интерфейса, а не светофор стадий: человеку не нужно знать,
// какой агент сейчас думает. Разноцветные стадии тащили в интерфейс сиреневый
// #9B8CFF — тот же барвинок, что и удалённый #7C8CFF, только другим кодом.
test("все видимые стадии светятся одним акцентом, а не своим оттенком", () => {
  const visible = (["linguistic", "geo", "context", "relaxation"] as const)
    .map((s) => STAGE_GLOW[s].color);
  expect(new Set(visible)).toEqual(new Set([PALETTE.evidence]));
});

test("ошибка гасится в нейтральный, а не в цвет происхождения", () => {
  expect(STAGE_GLOW.error.color).toBe(PALETTE.inkFaint);
});

test("у стадии мышления есть человеческая подпись", () => {
  expect(STAGE_GLOW.linguistic.caption).toBe("Разбираю запрос…");
});
