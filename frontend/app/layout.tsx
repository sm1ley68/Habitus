import type { Metadata } from "next";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import AuthGate from "@/components/auth/AuthGate";
import { ToastProvider } from "@/components/ui";
import "./globals.css";

export const metadata: Metadata = {
  title: "Urban Intelligence",
  description: "ИИ-агент для поиска жилья по жизненным сценариям",
};

// AuthGate стоит в корневом лэйауте, а не на отдельной странице: за сессией
// закрыт весь /api/v1, кроме auth/*, поэтому и кабинет, и рабочее пространство
// одинаково бессмысленны без входа.
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ru" className={`${GeistSans.variable} ${GeistMono.variable}`}>
      <body>
        <ToastProvider>
          <AuthGate>{children}</AuthGate>
        </ToastProvider>
      </body>
    </html>
  );
}
