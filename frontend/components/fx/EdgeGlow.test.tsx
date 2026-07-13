import { render, screen, act } from "@testing-library/react";
import EdgeGlow from "./EdgeGlow";
import { useSession } from "@/lib/store/session";

test("edge glow overlay is present and aria-hidden", () => {
  render(<EdgeGlow />);
  const glow = screen.getByTestId("edge-glow");
  expect(glow).toHaveAttribute("aria-hidden", "true");
});

test("entering a thinking stage sets a non-zero target opacity var", () => {
  render(<EdgeGlow />);
  act(() => { useSession.getState().applyEvent({ agent: "geo", status: "processing", message: "" }); });
  // GSAP animates toward the target; the data attribute records the intended target immediately.
  expect(screen.getByTestId("edge-glow").dataset.targetOpacity).toBe("1");
  expect(screen.getByTestId("edge-glow").dataset.stage).toBe("geo");
});
