import { render, screen, act, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ChatScreen from "./ChatScreen";
import { useSession } from "@/lib/store/session";

beforeEach(() => act(() => useSession.getState().reset()));

test("submitting a query drives the stage past idle and streams an answer", async () => {
  render(<ChatScreen />);
  await userEvent.type(screen.getByLabelText("Запрос агенту"), "тихий двор");
  await userEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
  await waitFor(() => expect(useSession.getState().stage).not.toBe("idle"), { timeout: 3000 });
  await waitFor(() => expect(useSession.getState().answer.length).toBeGreaterThan(0), { timeout: 8000 });
});

test("stage=error renders ErrorState", () => {
  act(() => useSession.setState({ stage: "error" }));
  render(<ChatScreen />);
  expect(screen.getByText("Что-то пошло не так")).toBeInTheDocument();
});
