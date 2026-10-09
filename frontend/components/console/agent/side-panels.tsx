"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { EconIntent, RailStatus, SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { IconGauge, IconShield, IconTag, IconWallet } from "@/components/icons";

function PanelHeading({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between px-5 pt-5 pb-3">
      <h2 className="text-[0.8rem] font-semibold tracking-wide text-foreground uppercase">{children}</h2>
      {action}
    </div>
  );
}

function Row({ icon, label, value, hint }: { icon: React.ReactNode; label: string; value: string; hint: string }) {
  return (
    <li className="flex gap-3 px-5 py-3">
      <span className="mt-0.5 text-muted [&>svg]:h-4 [&>svg]:w-4">{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline justify-between gap-3">
          <span className="text-sm text-foreground">{label}</span>
          <span className="shrink-0 font-mono text-sm text-primary tabular-nums">{value}</span>
        </div>
        <p className="mt-0.5 text-xs leading-snug text-muted">{hint}</p>
      </div>
    </li>
  );
}

/** The pass this chat pays under, the wallet that pays, and what the chat has spent. */
export function PassPanel({
  pass,
  rail,
  network,
  session,
}: {
  pass: SpendPass | null | undefined;
  rail: RailStatus | undefined;
  network: string;
  session: { spent: number; calls: number; steps: number };
}) {
  const used = pass ? pass.budget_minor_units - pass.remaining_minor_units : 0;
  const pct = pass && pass.budget_minor_units > 0 ? Math.min(100, (used / pass.budget_minor_units) * 100) : 0;

  return (
    <div className="flex h-full flex-col overflow-y-auto">
      <PanelHeading action={<Link href="/console/passes" className="text-xs font-medium text-primary hover:underline">Passes</Link>}>
        Spend Pass
      </PanelHeading>
      {pass === undefined ? (
        <div className="space-y-3 px-5">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-10 animate-pulse rounded-lg bg-border/60" />
          ))}
        </div>
      ) : pass === null ? (
        <p className="px-5 text-sm leading-relaxed text-muted">
          No pass picked. The agent can search the catalogs and read endpoints, but it can&apos;t pay until you pick a USDC pass under the chat box.
        </p>
      ) : (
        <ul className="divide-y divide-border border-y border-border">
          <li className="px-5 py-3">
            <p className="truncate text-sm font-medium text-foreground">{pass.label}</p>
            <div className="mt-2 flex gap-3">
              <span className="mt-0.5 text-muted">
                <IconGauge size={16} />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline justify-between gap-3">
                  <span className="text-sm text-foreground">Budget{pass.budget_period === "total" ? "" : ` / ${pass.budget_period}`}</span>
                  <span className="font-mono text-sm text-primary tabular-nums">{formatUSDC(pass.budget_minor_units)}</span>
                </div>
                <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-border">
                  <div
                    className={`h-full rounded-full transition-[width] duration-700 ${pct > 85 ? "bg-danger" : pct > 60 ? "bg-accent" : "bg-primary"}`}
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <p className="mt-1.5 text-xs text-muted">{formatUSDC(pass.remaining_minor_units)} left</p>
              </div>
            </div>
          </li>
          <Row
            icon={<IconTag />}
            label="Per call"
            value={pass.max_per_purchase_minor_units ? formatUSDC(pass.max_per_purchase_minor_units) : "Budget"}
            hint="Refused above this, before anything is paid"
          />
          <Row
            icon={<IconShield />}
            label="Approval"
            value={pass.approve_above_minor_units ? `≥ ${formatUSDC(pass.approve_above_minor_units)}` : "Never"}
            hint={pass.approve_above_minor_units ? "From this price, a call waits for your tap" : "Calls within the limits run on their own"}
          />
          <Row
            icon={<IconShield />}
            label="Providers"
            value={pass.allowed_merchants.length ? String(pass.allowed_merchants.length) : "Any"}
            hint={pass.allowed_merchants.length ? pass.allowed_merchants.slice(0, 3).join(", ") : "Any provider in the catalogs"}
          />
        </ul>
      )}

      <PanelHeading>Wallet · {networkLabel(network)}</PanelHeading>
      <div className="flex gap-3 border-y border-border px-5 py-3">
        <span className="mt-0.5 text-muted">
          <IconWallet size={16} />
        </span>
        {!rail ? (
          <div className="h-8 flex-1 animate-pulse rounded-lg bg-border/60" />
        ) : !rail.configured ? (
          <p className="text-xs leading-relaxed text-muted">
            No {networkLabel(network).toLowerCase()} wallet on this server. Calls can be searched and priced, not paid.
          </p>
        ) : (
          <div className="min-w-0 flex-1">
            <p className="truncate font-mono text-xs text-foreground" title={rail.address}>
              {rail.address}
            </p>
            <p className="mt-1 text-xs text-muted">
              {rail.error ? "Node unreachable right now" : rail.usdc_minor != null ? `${formatUSDC(rail.usdc_minor)} available` : "No USDC yet"}
              {rail.max_payment_minor ? ` · at most ${formatUSDC(rail.max_payment_minor)} a call` : ""}
            </p>
          </div>
        )}
      </div>

      <PanelHeading>This chat</PanelHeading>
      <dl className="grid grid-cols-3 gap-px overflow-hidden border-y border-border bg-border">
        {[
          { k: "Paid", v: formatUSDC(session.spent).replace(" USDC", "") },
          { k: "Calls", v: String(session.calls) },
          { k: "Steps", v: String(session.steps) },
        ].map((s) => (
          <div key={s.k} className="bg-background px-3 py-3 text-center">
            <dt className="text-[0.7rem] text-muted">{s.k}</dt>
            <dd className="mt-0.5 font-mono text-sm text-foreground tabular-nums">{s.v}</dd>
          </div>
        ))}
      </dl>
      <p className="mt-auto px-5 py-5 text-xs leading-relaxed text-muted">
        The pass is checked on Algebra&apos;s server before every payment. The agent can read it; it can&apos;t change it or approve for you.
      </p>
    </div>
  );
}

function stateTone(state: string) {
  if (state === "COMMITTED") return "bg-primary";
  if (state.includes("UNKNOWN") || state.includes("APPROVAL")) return "bg-accent";
  if (state === "FAILED" || state === "CANCELLED" || state === "EXPIRED") return "bg-border-strong";
  return "bg-primary/50";
}

/** The person's latest paid calls, from any agent. */
export function RecentCallsPanel({ refreshKey }: { refreshKey: number }) {
  const [intents, setIntents] = useState<EconIntent[] | null>(null);

  useEffect(() => {
    api
      .listMyEconomicIntents(8)
      .then((r) => setIntents(r.intents ?? []))
      .catch(() => setIntents([]));
  }, [refreshKey]);

  return (
    <div className="flex h-full flex-col overflow-y-auto">
      <PanelHeading action={<Link href="/console/executions" className="text-xs font-medium text-primary hover:underline">All</Link>}>
        Recent paid calls
      </PanelHeading>
      {intents === null && (
        <div className="space-y-3 px-5">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-10 animate-pulse rounded-lg bg-border/60" />
          ))}
        </div>
      )}
      {intents && intents.length === 0 && <p className="px-5 text-sm text-muted">No paid calls yet. The first one lands here, with its receipt.</p>}
      {intents && intents.length > 0 && (
        <ul className="divide-y divide-border border-y border-border">
          {intents.map((i) => (
            <li key={i.id}>
              <Link href="/console/executions" className="flex items-center gap-3 px-5 py-3 hover:bg-primary-tint/40">
                <span className={`h-2 w-2 shrink-0 rounded-full ${stateTone(i.state)}`} aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-xs text-foreground">{i.capability}</p>
                  <p className="text-xs text-muted">
                    {i.state.toLowerCase().replace(/_/g, " ")} · {new Date(i.created_at).toLocaleDateString(undefined, { month: "short", day: "numeric" })}
                  </p>
                </div>
                <span className="shrink-0 font-mono text-xs text-foreground tabular-nums">
                  {i.committed_minor > 0 ? formatUSDC(i.committed_minor) : "—"}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
