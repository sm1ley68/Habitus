import AppShell from "@/components/shell/AppShell";
import AuthGate from "@/components/auth/AuthGate";

export default function Page() {
  return (
    <AuthGate>
      <AppShell />
    </AuthGate>
  );
}
