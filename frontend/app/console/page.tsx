"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import { firstName, useSession } from "@/lib/session";
import type { EconIntent, EconStats, ProviderListing, SpendPass } from "@/lib/types";
import { formatMoney, formatUSDC } from "@/lib/money";
import { catalogName, joinNames } from "@/lib/paysh";
import { ErrorNote, Skeleton, StatusBadge, timeAgo } from "@/components/console/ui";
import { IconArrowRight, IconCheck, IconInbox, IconPlug, IconShield, IconStore } from "@/components/icons";

function greeting() {
  const h = new Date().getHours();
  return h < 5 ? "Working late" : h < 12 ? "Good morning" : h < 17 ? "Good afternoon" : "Good evening";
}

export default function OverviewPage() {
  const { user } = useSession();
  const [intents, setIntents] = useState<EconIntent[] | null>(null);
  const [stats, setStats] = useState<EconStats | null>(null);
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [catalog, setCatalog] = useState<ProviderListing | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([api.listMyEconomicIntents(8), api.getMyEconomicStats(30), api.listPasses()])
      .then(([l, s, p]) => {
        setIntents(l.intents ?? []);
        setStats(s.stats);
        setPasses(p.passes);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load your overview"));
    // The catalogs are public and slower to read the first time; they never block the page.
    api.listProviders({ limit: 1 }).then(setCatalog).catch(() => {});
  }, []);

  const active = passes?.filter((p) => p.active && p.currency === "USDC") ?? [];
  const waiting = intents?.filter((i) => i.state === "AWAITING_APPROVAL").length ?? 0;
  const fresh = passes !== null && passes.filter((p) => p.currency === "USDC").length === 0;

  return (
    <div className="console-overview mx-auto">
      <div className="flex flex-col gap-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p className="mb-3 font-mono text-[0.625rem] tracking-[0.15em] text-muted">YOUR AGENT OPERATIONS</p>
          <h1 className="font-display text-[2rem] leading-tight font-semibold tracking-tight text-foreground">
            {greeting()}{firstName(user) ? `, ${firstName(user)}` : ""}
          </h1>
          <p className="mt-1.5 text-[0.95rem] text-muted">
            {waiting > 0
              ? `${waiting} request${waiting === 1 ? " is" : "s are"} waiting for your approval.`
              : "Your agents pay for APIs in USDC on Solana, inside the limits you set."}
          </p>
        </div>
        <Link
          href="/console/providers"
          className="inline-flex items-center gap-2 rounded-xl bg-primary px-4 py-2.5 text-sm font-medium text-primary-tint hover:opacity-90"
        >
          <IconStore size={16} /> Browse providers
        </Link>
      </div>

      {error && <div className="mt-6"><ErrorNote>{error}</ErrorNote></div>}

      {waiting > 0 && (
        <Link
          href="/console/executions"
          className="mt-6 flex items-center gap-3 rounded-2xl border border-accent/60 bg-accent-tint/40 px-5 py-4 text-sm text-foreground hover:bg-accent-tint/60"
        >
          <IconInbox size={18} className="text-accent" />
          <span className="flex-1">
            {waiting} request{waiting === 1 ? "" : "s"} above a pass&apos;s ask-me line {waiting === 1 ? "needs" : "need"} your yes or no.
          </span>
          <IconArrowRight size={16} />
        </Link>
      )}

      {fresh && <GettingStarted providers={catalog?.total} />}

      <dl className="mt-8 grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Spent, 30 days" value={stats ? formatUSDC(stats.spent_minor) : null} />
        <Stat label="Paid calls" value={stats ? String(stats.committed) : null} />
        <Stat label="Duplicate payments stopped" value={stats ? String(stats.duplicate_commit_attempts_blocked) : null} />
        <Stat label="Active passes" value={passes ? String(active.length) : null} />
      </dl>

      {passes && active.length > 0 && <section className="live-budget-panel" aria-labelledby="budget-heading"><div className="live-budget-heading"><h2 id="budget-heading">Spending authority</h2><span>Live spend pass balances</span></div><div className="live-budget-bars">{active.slice(0, 4).map((pass) => {
        const used = pass.budget_minor_units > 0 ? Math.min(pass.spent_minor_units / pass.budget_minor_units * 100, 100) : 0;
        return <div className="live-budget-row" key={pass.id}><div><strong>{pass.label}</strong><span>{formatMoney(pass.spent_minor_units, pass.currency)} / {formatMoney(pass.budget_minor_units, pass.currency)}</span></div><div className="live-budget-track" role="meter" aria-label={`${pass.label} budget used`} aria-valuenow={Math.round(used)} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${used}%` }} /></div></div>;
      })}</div><p>Each agent can spend only within its pass. <Link href="/console/passes" className="font-medium text-foreground underline">Manage spending limits</Link></p></section>}

      <div className="mt-8 grid gap-6 md:grid-cols-[1.3fr_1fr]">
        <section aria-labelledby="recent-heading">
          <div className="flex items-baseline justify-between">
            <h2 id="recent-heading" className="font-display text-base font-semibold text-foreground">
              Recent executions
            </h2>
            <Link href="/console/executions" className="text-xs font-medium text-primary hover:underline">
              All executions
            </Link>
          </div>
          <div className="mt-3 rounded-2xl border border-border bg-surface">
            {!intents && !error && <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-10" />)}</div>}
            {intents?.length === 0 && <p className="px-5 py-8 text-center text-sm text-muted">No paid calls yet. They appear here as your agents make them.</p>}
            {intents && intents.length > 0 && (
              <ul className="divide-y divide-border">
                {intents.slice(0, 6).map((i) => (
                  <li key={i.id} className="flex items-center gap-3 px-4 py-3">
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-xs text-foreground">{i.capability}</span>
                      <span className="block text-[0.6875rem] text-muted">{timeAgo(i.created_at)}</span>
                    </span>
                    <span className="font-mono text-xs text-foreground tabular-nums">{i.committed_minor > 0 ? formatUSDC(i.committed_minor) : "—"}</span>
                    <StatusBadge status={i.state} />
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>

        <section aria-labelledby="passes-heading">
          <div className="flex items-baseline justify-between">
            <h2 id="passes-heading" className="font-display text-base font-semibold text-foreground">
              Spend passes
            </h2>
            <Link href="/console/passes" className="text-xs font-medium text-primary hover:underline">
              Manage
            </Link>
          </div>
          <div className="mt-3 space-y-2">
            {!passes && !error && <Skeleton className="h-20" />}
            {passes && active.length === 0 && (
              <p className="rounded-2xl border border-dashed border-border-strong px-5 py-6 text-center text-sm text-muted">
                No active USDC pass. An agent needs one to spend.
              </p>
            )}
            {active.slice(0, 4).map((p) => {
              const used = p.budget_minor_units > 0 ? Math.min(p.spent_minor_units / p.budget_minor_units, 1) : 0;
              return (
                <div key={p.id} className="rounded-2xl border border-border bg-surface px-4 py-3">
                  <div className="flex items-baseline justify-between gap-3">
                    <p className="truncate text-sm font-medium text-foreground">{p.label}</p>
                    <p className="shrink-0 font-mono text-xs text-muted tabular-nums">{formatMoney(p.remaining_minor_units, p.currency)} left</p>
                  </div>
                  <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-border">
                    <div className={`h-full rounded-full ${used >= 0.9 ? "bg-danger" : "bg-primary"}`} style={{ width: `${used * 100}%` }} />
                  </div>
                </div>
              );
            })}
          </div>
          {catalog?.sources && (
            <p className="mt-4 text-xs text-muted">
              {catalog.total} provider{catalog.total === 1 ? "" : "s"} available from{" "}
              {joinNames(catalog.sources.filter((s) => !s.error).map((s) => catalogName(s.name)))}.
            </p>
          )}
        </section>
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | null }) {
  return (
    <div className="rounded-xl border border-border bg-surface px-4 py-3">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="font-display mt-1 text-lg font-semibold text-foreground tabular-nums">{value ?? <Skeleton className="h-6 w-16" />}</dd>
    </div>
  );
}

function GettingStarted({ providers }: { providers?: number }) {
  const steps = [
    { href: "/console/passes", icon: <IconShield size={16} />, title: "Issue a Spend Pass", body: "A USDC budget, the most one call may cost, and which providers it may pay." },
    { href: "/console/connect", icon: <IconPlug size={16} />, title: "Connect your agent", body: "Claude, Cursor, the OpenAI Agents SDK or any HTTP client, over MCP or REST." },
    {
      href: "/console/providers",
      icon: <IconStore size={16} />,
      title: "Pick what it can call",
      body: providers ? `${providers} paid APIs from Pay.sh, Circle's Agent Marketplace, PayAI and Coinbase's Bazaar.` : "Paid APIs from Pay.sh, Circle's Agent Marketplace, PayAI and Coinbase's Bazaar.",
    },
  ];
  return (
    <section aria-label="Getting started" className="mt-6 rounded-2xl border border-border bg-surface p-5">
      <p className="text-sm font-medium text-foreground">Three steps to your agent&apos;s first paid call</p>
      <ol className="mt-4 grid gap-3 md:grid-cols-3">
        {steps.map((s, i) => (
          <li key={s.href}>
            <Link href={s.href} className="flex h-full flex-col rounded-xl border border-border px-4 py-3 hover:border-primary/50 hover:bg-primary-tint/40">
              <span className="flex items-center gap-2 text-sm font-medium text-foreground">
                <span className="grid h-6 w-6 place-items-center rounded-full bg-primary-tint text-xs text-primary">{i + 1}</span>
                {s.title}
              </span>
              <span className="mt-1.5 text-xs leading-relaxed text-muted">{s.body}</span>
            </Link>
          </li>
        ))}
      </ol>
      <p className="mt-3 flex items-center gap-1.5 text-xs text-muted">
        <IconCheck size={12} className="text-primary" /> Until this server has a funded Solana wallet, only the sandbox provider can be paid, with simulated USDC.
      </p>
    </section>
  );
}
