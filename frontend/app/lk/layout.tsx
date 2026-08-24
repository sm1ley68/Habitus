import CabinetShell from "@/components/owner/CabinetShell";

export default function CabinetLayout({ children }: { children: React.ReactNode }) {
  return <CabinetShell>{children}</CabinetShell>;
}
