import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ResultFeedback from "./ResultFeedback";
import { useSession } from "@/lib/store/session";
import { useEngagement } from "@/lib/store/engagement";

const feedbackApi = vi.hoisted(() => ({ saveFeedback: vi.fn() }));
vi.mock("@/lib/api/feedback", () => feedbackApi);

beforeEach(() => {
  vi.clearAllMocks();
  useSession.setState({ chatId: "chat-1" });
  useEngagement.setState({ verdicts: {}, verdictsChatId: null });
});

describe("ResultFeedback", () => {
  it("без chat_id не рисуется: оценивать вне чата шлюз всё равно не даст", () => {
    useSession.setState({ chatId: null });
    const { container } = render(<ResultFeedback objectId="obj-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("«подходит» уходит сразу, без причины", async () => {
    feedbackApi.saveFeedback.mockResolvedValue(undefined);
    render(<ResultFeedback objectId="obj-1" />);
    await userEvent.click(screen.getByRole("button", { name: "Подходит" }));
    expect(feedbackApi.saveFeedback)
      .toHaveBeenCalledWith("chat-1", "obj-1", "up", undefined);
  });

  it("«не подходит» спрашивает причину и отправляет её", async () => {
    feedbackApi.saveFeedback.mockResolvedValue(undefined);
    render(<ResultFeedback objectId="obj-1" />);
    await userEvent.click(screen.getByRole("button", { name: "Не подходит" }));
    await userEvent.type(screen.getByLabelText("Почему не подходит"), "далеко от метро");
    await userEvent.click(screen.getByRole("button", { name: "Отправить" }));
    expect(feedbackApi.saveFeedback)
      .toHaveBeenCalledWith("chat-1", "obj-1", "down", "далеко от метро");
  });

  it("оценка из другого чата не подсвечивается", () => {
    useEngagement.setState({ verdicts: { "obj-1": "up" }, verdictsChatId: "chat-0" });
    render(<ResultFeedback objectId="obj-1" />);
    expect(screen.getByRole("button", { name: "Подходит" }))
      .toHaveAttribute("aria-pressed", "false");
  });
});
