import type { Metadata } from "next";
import { SessionProvider } from "@/lib/session";
import { OnboardingFlow } from "@/components/onboarding/flow";

export const metadata: Metadata = { title: "Welcome — Algebra" };

export default function OnboardingPage() {
  return (
    <SessionProvider required>
      <OnboardingFlow />
    </SessionProvider>
  );
}
