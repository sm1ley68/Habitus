import { STAGE_GLOW } from "./stageVisuals";

test("idle is invisible, done is invisible", () => {
  expect(STAGE_GLOW.idle.opacity).toBe(0);
  expect(STAGE_GLOW.done.opacity).toBe(0);
});
test("each thinking stage has its own hue and a caption", () => {
  expect(STAGE_GLOW.linguistic.color).toBe("#7C8CFF");
  expect(STAGE_GLOW.geo.color).toBe("#5AB8E0");
  expect(STAGE_GLOW.context.color).toBe("#9B8CFF");
  expect(STAGE_GLOW.error.color).toBe("#9BAAB8");
  expect(STAGE_GLOW.linguistic.caption).toBe("Разбираю запрос…");
});
