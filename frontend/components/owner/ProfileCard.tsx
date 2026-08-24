"use client";
import { useState } from "react";
import { Button, Card } from "@/components/ui";
import { logout } from "@/lib/api/auth";
import type { User } from "@/lib/api/auth";

export default function ProfileCard({ user }: { user: User }) {
  const [busy, setBusy] = useState(false);

  const signOut = async () => {
    setBusy(true);
    try {
      await logout();
    } finally {
      // Полная перезагрузка, а не router.push: она сбрасывает всё клиентское
      // состояние сессии заодно с кукой.
      window.location.assign("/");
    }
  };

  return (
    <Card className="max-w-md p-6">
      <p className="text-[15px] tracking-tight text-[#1c1d20]">{user.name}</p>
      <p className="mt-1 font-mono text-sm text-zinc-500">{user.email}</p>

      <div className="mt-6">
        <Button variant="secondary" loading={busy} onClick={signOut}>
          Выйти
        </Button>
      </div>
    </Card>
  );
}
