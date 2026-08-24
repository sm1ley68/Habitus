import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import ProfileCard from "./ProfileCard";

vi.mock("@/lib/api/auth", () => ({
  logout: vi.fn().mockResolvedValue(undefined),
}));

test("показывает имя и почту и умеет выходить", async () => {
  const { logout } = await import("@/lib/api/auth");
  render(<ProfileCard user={{ id: "1", email: "seller@example.com", name: "Продавец" }} />);

  expect(screen.getByText("Продавец")).toBeInTheDocument();
  expect(screen.getByText("seller@example.com")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: /выйти/i }));
  expect(logout).toHaveBeenCalled();
});
