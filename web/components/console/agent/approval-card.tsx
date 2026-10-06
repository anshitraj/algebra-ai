"use client";

import { useEffect, useState } from "react";
import { motion } from "motion/react";
import * as api from "@/lib/api-client";
import type { EconIntent } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { IconShield, Spinner } from "@/components/icons";

export type ApprovalOutcome = "approved" | "rejected";

/**
 * The human half of approval_required. Loads the real intent (its capability
 * and its ceiling) with the person's session and approves or cancels it with
 * the same endpoints the Executions page uses — the agent has no tool that
 * can do either.
 */
export function ApprovalCard({
  intentId,
  provider,
  resolved,
  onResolved,
}: {
  intentId: string;
  provider?: string;
  resolved?: ApprovalOutcome;
  onResolved: (o: ApprovalOutcome) => void;
}) {
  const [intent, setIntent] = useState<EconIntent | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<ApprovalOutcome | null>(null);

  useEffect(() => {
    api
      .getMyEconomicIntent(intentId)
      .then(setIntent)
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the call"));
  }, [intentId]);

  async function decide(o: ApprovalOutcome) {
    setBusy(o);
    setError(null);
    try {
      if (o === "approved") await api.approveEconomicIntent(intentId);
      else await api.cancelEconomicIntent(intentId);
      onResolved(o);
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't go through");
    } finally {
      setBusy(null);
    }
  }

  // The call stops needing the person once it leaves AWAITING_APPROVAL: they
  // approved or cancelled it elsewhere (the Executions page), or it expired.
  // requires_approval can't say this: it stays true after approval.
  const closed = !!intent && intent.state !== "AWAITING_APPROVAL" && !resolved;

  return (
    <motion.div
      initial={{ opacity: 0, y: 8, scale: 0.99 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      transition={{ duration: 0.35, ease: [0.16, 1, 0.3, 1] }}
      className={`rounded-2xl border p-4 sm:p-5 ${resolved || closed ? "border-border bg-surface" : "border-accent/60 bg-accent-tint/40"}`}
    >
      <div className="flex items-start gap-3">
        <span
          className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl ${resolved || closed ? "bg-primary-tint text-primary" : "bg-accent text-accent-tint"}`}
        >
          <IconShield size={18} />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-[0.95rem] font-medium text-foreground">
            {resolved === "approved"
              ? "You approved this call"
              : resolved === "rejected"
                ? "You cancelled this call"
                : closed
                  ? "This call no longer needs you"
                  : "Approve this paid call?"}
          </p>
          {intent ? (
            <p className="mt-0.5 text-sm text-muted">
              <span className="font-mono text-xs text-foreground">{intent.capability}</span>
              {provider && <> · {provider}</>} · up to <span className="font-mono text-foreground tabular-nums">{formatUSDC(intent.budget_max_minor)}</span>
            </p>
          ) : !error ? (
            <p className="mt-0.5 text-sm text-muted">Loading the exact terms…</p>
          ) : null}
          {intent && !resolved && !closed && (
            <p className="mt-2 text-xs text-muted">
              Your Spend Pass asks you to approve calls at this price. Algebra still checks the endpoint&apos;s real price against this ceiling before
              it pays, and pays once at most.
            </p>
          )}
        </div>
      </div>
      {error && <p className="mt-3 rounded-lg bg-danger-tint px-3 py-2 text-sm text-danger">{error}</p>}
      {!resolved && !closed && intent && (
        <div className="mt-4 flex flex-wrap gap-2.5 sm:pl-12">
          <button
            type="button"
            disabled={!!busy}
            onClick={() => decide("approved")}
            className="inline-flex h-10 items-center gap-2 rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint transition-[opacity,transform] hover:opacity-95 active:scale-[0.98] disabled:opacity-45"
          >
            {busy === "approved" && <Spinner size={14} />} Approve up to {formatUSDC(intent.budget_max_minor)}
          </button>
          <button
            type="button"
            disabled={!!busy}
            onClick={() => decide("rejected")}
            className="inline-flex h-10 items-center gap-2 rounded-xl border border-border-strong px-4 text-sm font-medium text-foreground transition-colors hover:bg-surface disabled:opacity-45"
          >
            {busy === "rejected" && <Spinner size={14} />} Cancel the call
          </button>
        </div>
      )}
    </motion.div>
  );
}
