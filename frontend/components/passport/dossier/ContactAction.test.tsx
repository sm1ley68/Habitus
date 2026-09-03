import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ContactAction from "./ContactAction";
import { useAuth } from "@/lib/store/auth";
import { LeadError } from "@/lib/api/lead";

const lead = vi.hoisted(() => ({ sendLead: vi.fn() }));
vi.mock("@/lib/api/lead", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/lead")>();
  return { ...actual, sendLead: lead.sendLead };
});

beforeEach(() => {
  vi.clearAllMocks();
  useAuth.setState({
    user: { id: "u1", email: "a@b.c", name: "Аня", is_guest: false },
    status: "ready", authOpen: false,
  });
});

describe("ContactAction", () => {
  it("при kind=none кнопки нет — выдуманная вела бы в никуда", () => {
    const { container } = render(
      <ContactAction objectId="obj-1" contact={{ kind: "none" }} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("без поля contact ничего не рисует", () => {
    const { container } = render(<ContactAction objectId="obj-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("при kind=external уводит на источник", () => {
    render(<ContactAction objectId="obj-1" contact={{
      kind: "external", source_url: "https://www.cian.ru/sale/flat/1/",
    }} />);
    const link = screen.getByRole("link", { name: /Открыть на источнике/ });
    expect(link).toHaveAttribute("href", "https://www.cian.ru/sale/flat/1/");
  });

  it("при kind=lead отправляет заявку", async () => {
    lead.sendLead.mockResolvedValue({ lead: { id: "l1" }, registered: false });
    render(<ContactAction objectId="obj-1" contact={{ kind: "lead" }} />);
    await userEvent.type(screen.getByLabelText("Имя"), "Иван");
    await userEvent.type(screen.getByLabelText("Контакт"), "+7 999 000-00-00");
    await userEvent.click(screen.getByRole("button", { name: "Отправить заявку" }));
    expect(lead.sendLead).toHaveBeenCalledWith(
      "obj-1", { name: "Иван", contact: "+7 999 000-00-00", message: "" }, undefined);
    expect(await screen.findByText(/Заявка отправлена/)).toBeInTheDocument();
  });

  it("гостю поля аккаунта показаны сразу — форма не теряется на 403", () => {
    useAuth.setState({
      user: { id: "g1", email: "", name: "Гость", is_guest: true },
      status: "ready", authOpen: false,
    });
    render(<ContactAction objectId="obj-1" contact={{ kind: "lead" }} />);
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
    expect(screen.getByLabelText("Пароль")).toBeInTheDocument();
  });

  it("registration_required раскрывает поля, ничего не очищая", async () => {
    lead.sendLead.mockRejectedValue(
      new LeadError("registration_required", "Зарегистрируйтесь, чтобы отправить заявку"));
    render(<ContactAction objectId="obj-1" contact={{ kind: "lead" }} />);
    await userEvent.type(screen.getByLabelText("Имя"), "Иван");
    await userEvent.type(screen.getByLabelText("Контакт"), "+7 999 000-00-00");
    await userEvent.click(screen.getByRole("button", { name: "Отправить заявку" }));
    expect(await screen.findByLabelText("Email")).toBeInTheDocument();
    expect(screen.getByLabelText("Имя")).toHaveValue("Иван");
    expect(screen.getByLabelText("Контакт")).toHaveValue("+7 999 000-00-00");
  });
});
