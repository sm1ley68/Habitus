import type { Metadata } from "next";
import { Literata, Golos_Text } from "next/font/google";
import AuthGate from "@/components/auth/AuthGate";
import { ToastProvider } from "@/components/ui";
import "./globals.css";

// Literata — витринный шрифт (заголовки, досье), Golos Text — интерфейсный.
// Оба варианта регистрируются как CSS-переменные и читаются из globals.css.
const display = Literata({
  subsets: ["cyrillic", "latin"],
  variable: "--font-display",
  weight: ["400", "500", "600"],
  display: "swap",
});
const ui = Golos_Text({
  subsets: ["cyrillic", "latin"],
  variable: "--font-ui",
  weight: ["400", "500", "600"],
  display: "swap",
});

export const metadata: Metadata = {
  title: "Habitus",
  description: "ИИ-агент для поиска жилья по жизненным сценариям",
};

// AuthGate стоит в корневом лэйауте, а не на отдельной странице: за сессией
// закрыт весь /api/v1, кроме auth/*, поэтому и кабинет, и рабочее пространство
// одинаково бессмысленны без входа.
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ru" className={`${display.variable} ${ui.variable}`}>
      <body>
        <ToastProvider>
          <AuthGate>{children}</AuthGate>
        </ToastProvider>
      </body>
    </html>
  );
}
