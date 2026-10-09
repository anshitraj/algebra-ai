"use client";

import { ErrorState } from "@/components/error-state";

// Keeps the console shell (nav, guardrails) around a page that crashed.
export default function ConsoleError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return <ErrorState error={error} retry={retry} home="/console" />;
}
