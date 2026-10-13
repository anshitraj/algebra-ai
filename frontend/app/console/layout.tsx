import type { Metadata } from "next";
import { SessionProvider } from "@/lib/session";
import { ConsoleShell } from "@/components/console/shell";

export const metadata: Metadata = { title: "Console — Algebra" };

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="console-theme">
      <SessionProvider required requireOnboarded>
        <ConsoleShell>{children}</ConsoleShell>
      </SessionProvider>
    </div>
  );
}
