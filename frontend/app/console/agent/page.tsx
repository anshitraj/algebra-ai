import type { Metadata } from "next";
import { Suspense } from "react";
import { AgentWorkspace } from "@/components/console/agent/workspace";

export const metadata: Metadata = { title: "Agent — Algebra" };

export default function AgentPage() {
  return (
    <Suspense>
      <AgentWorkspace />
    </Suspense>
  );
}
