import { gradeTone } from "./grade";

test("A grades are green, B amber, else warm", () => {
  expect(gradeTone("A+").color).toBe("#2f8f5f");
  expect(gradeTone("B").color).toBe("#b3822f");
  expect(gradeTone("C").color).toBe("#b25e4a");
});
