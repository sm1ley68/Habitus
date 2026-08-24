"use client";
import { useEffect, useState } from "react";
import ProfileCard from "@/components/owner/ProfileCard";
import { me, type User } from "@/lib/api/auth";

export default function ProfilePage() {
  const [user, setUser] = useState<User | null>(null);

  useEffect(() => {
    let alive = true;
    me()
      .then((u) => {
        if (alive) setUser(u);
      })
      .catch(() => {
        if (alive) setUser(null);
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!user) return <p className="py-16 text-sm text-zinc-400">Загружаем профиль…</p>;

  return <ProfileCard user={user} />;
}
