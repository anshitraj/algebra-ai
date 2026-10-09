"use client";

import Link from "next/link";
import { useEffect } from "react";

/**
 * What a crashed page shows instead of a blank screen: what happened, a way
 * to retry, and a reference that matches the server log (never the raw
 * error — production messages from server components are generic anyway).
 */
export function ErrorState({ error, retry, home = "/" }: { error: Error & { digest?: string }; retry: () => void; home?: string }) {
  useEffect(() => {
    console.error(error);
  }, [error]);
  return (
    <div role="alert" className="mx-auto flex max-w-md flex-col items-start px-5 py-24">
      <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">This page hit a problem</h1>
      <p className="mt-2 text-[0.95rem] leading-relaxed text-muted">
        Nothing you approved was changed. Try again — if it keeps happening, sign out and back in, or contact us with the reference below.
      </p>
      {error.digest && <p className="mt-3 font-mono text-xs text-muted">Reference: {error.digest}</p>}
      <div className="mt-6 flex gap-2.5">
        <button type="button" onClick={() => retry()} className="inline-flex h-10 items-center rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint">
          Try again
        </button>
        <Link href={home} className="inline-flex h-10 items-center rounded-xl border border-border-strong px-4 text-sm font-medium text-foreground hover:bg-surface">
          Go back
        </Link>
      </div>
    </div>
  );
}
