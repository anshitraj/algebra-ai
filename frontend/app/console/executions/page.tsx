"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { EconIntent, EconStats, KeptResult } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { Button, ErrorNote, PageHeader, Panel, Skeleton, StatusBadge, timeAgo } from "@/components/console/ui";
import { Copy } from "@/components/console/snippet";
import { IconChevronDown, IconInbox, Spinner } from "@/components/icons";

export default function ExecutionsPage() {
  const [intents, setIntents] = useState<EconIntent[] | null>(null);
  const [stats, setStats] = useState<EconStats | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [l, s] = await Promise.all([api.listMyEconomicIntents(50), api.getMyEconomicStats(30)]);
      setIntents(l.intents ?? []);
      setStats(s.stats);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load your executions");
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    load();
  }, [load]);

  const waiting = intents?.filter((i) => i.state === "AWAITING_APPROVAL") ?? [];
  const rest = intents?.filter((i) => i.state !== "AWAITING_APPROVAL") ?? [];

  return (
    <div className="mx-auto max-w-4xl">
      <PageHeader
        title="Executions"
        description="Everything your agents asked Algebra to get done and pay for: what it cost, who was paid, how the payment was settled, and the signed receipt."
      />

      {stats && <Stats stats={stats} />}

      <div className="mt-8 space-y-3">
        {error && <ErrorNote>{error}</ErrorNote>}
        {!intents && !error && [0, 1, 2].map((i) => <Skeleton key={i} className="h-16" />)}

        {waiting.length > 0 && (
          <section aria-labelledby="waiting-heading" className="space-y-3">
            <h2 id="waiting-heading" className="flex items-center gap-2 text-sm font-medium text-foreground">
              <IconInbox size={16} className="text-accent" /> Waiting for your approval
            </h2>
            {waiting.map((i) => (
              <ApprovalCard key={i.id} intent={i} onDone={load} />
            ))}
          </section>
        )}

        {intents && intents.length === 0 && (
          <div className="rounded-2xl border border-dashed border-border-strong px-6 py-14 text-center">
            <p className="font-display text-lg font-semibold text-foreground">Nothing yet</p>
            <p className="mx-auto mt-1.5 max-w-md text-sm text-muted">
              When an agent with one of your Spend Passes asks Algebra for something, it shows up here: the quote, the payment,
              the result&apos;s verdict and the receipt.
            </p>
            <div className="mt-5 flex justify-center gap-3 text-sm">
              <Link href="/console/connect" className="font-medium text-primary hover:underline">
                Connect an agent
              </Link>
              <Link href="/console/providers" className="font-medium text-primary hover:underline">
                Browse providers
              </Link>
            </div>
          </div>
        )}

        {rest.map((i) => (
          <IntentRow key={i.id} intent={i} open={open === i.id} onToggle={() => setOpen(open === i.id ? null : i.id)} />
        ))}
      </div>
    </div>
  );
}

function Stats({ stats }: { stats: EconStats }) {
  const items = [
    { label: "Spent, 30 days", value: formatUSDC(stats.spent_minor) },
    { label: "Paid calls", value: String(stats.committed) },
    { label: "Duplicate payments stopped", value: String(stats.duplicate_commit_attempts_blocked) },
    { label: "Being reconciled", value: String(stats.unresolved) },
  ];
  return (
    <dl className="mt-6 grid grid-cols-2 gap-3 md:grid-cols-4">
      {items.map((s) => (
        <div key={s.label} className="rounded-xl border border-border bg-surface px-4 py-3">
          <dt className="text-xs text-muted">{s.label}</dt>
          <dd className="font-display mt-1 text-lg font-semibold text-foreground tabular-nums">{s.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** The provider the intent's latest attempt went to. */
function providerOf(i: EconIntent) {
  const r = i.reservations?.[i.reservations.length - 1];
  return r?.provider_id ?? "";
}

function IntentRow({ intent: i, open, onToggle }: { intent: EconIntent; open: boolean; onToggle: () => void }) {
  const provider = providerOf(i);
  return (
    <div className={`rounded-2xl border bg-surface ${open ? "border-primary/40" : "border-border"}`}>
      <button type="button" onClick={onToggle} aria-expanded={open} className="flex w-full items-center gap-4 px-5 py-4 text-left">
        <span className="min-w-0 flex-1">
          <span className="block truncate font-mono text-sm text-foreground">{i.capability}</span>
          <span className="mt-0.5 block truncate text-xs text-muted">
            {provider ? `${provider} · ` : ""}
            {timeAgo(i.created_at)}
          </span>
        </span>
        <span className="hidden shrink-0 text-right sm:block">
          <span className="block font-mono text-xs text-foreground tabular-nums">
            {i.committed_minor > 0 ? formatUSDC(i.committed_minor) : `up to ${formatUSDC(i.budget_max_minor)}`}
          </span>
        </span>
        <StatusBadge status={i.state} />
        <IconChevronDown size={18} className={`shrink-0 text-muted transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      {open && <IntentDetail id={i.id} />}
    </div>
  );
}

function IntentDetail({ id }: { id: string }) {
  const [d, setD] = useState<EconIntent | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .getMyEconomicIntent(id)
      .then((x) => live && setD(x))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Couldn't load this execution"));
    return () => {
      live = false;
    };
  }, [id]);

  if (error) return <div className="border-t border-border px-5 py-4"><ErrorNote>{error}</ErrorNote></div>;
  if (!d)
    return (
      <p className="flex items-center gap-2 border-t border-border px-5 py-4 text-sm text-muted">
        <Spinner size={14} /> Loading…
      </p>
    );
  return (
    <div className="space-y-4 border-t border-border px-5 pt-4 pb-5">
      <p className="text-sm text-foreground">{d.summary}</p>
      <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-3">
        <Fact label="State" value={`${d.state}${d.commitment ? ` · ${d.commitment}` : ""}${d.fulfillment ? ` · ${d.fulfillment}` : ""}`} />
        <Fact label="Budget" value={formatUSDC(d.budget_max_minor)} />
        <Fact label="Attempts" value={`${d.attempts}${d.duplicate_commit_attempts_blocked ? ` · ${d.duplicate_commit_attempts_blocked} duplicate stopped` : ""}`} />
      </dl>
      {d.reservations?.length > 0 && (
        <div>
          <p className="text-xs font-medium text-muted">Attempts</p>
          <ul className="mt-1.5 divide-y divide-border rounded-xl border border-border">
            {d.reservations.map((r) => (
              <li key={r.id} className="grid gap-1 px-3.5 py-2.5 text-xs sm:grid-cols-[auto_1fr_auto] sm:items-center sm:gap-4">
                <span className="font-mono text-muted">#{r.attempt}</span>
                <span className="min-w-0">
                  <span className="block truncate text-foreground">{r.provider_id ?? "provider not chosen yet"}</span>
                  <span className="block truncate font-mono text-[0.6875rem] text-muted">
                    {r.evidence?.network ? `${r.evidence.network} · ` : ""}
                    {r.evidence?.transaction ? `tx ${r.evidence.transaction}` : r.rail ?? ""}
                    {r.evidence?.test ? " · simulated (sandbox)" : ""}
                  </span>
                </span>
                <span className="flex items-center gap-2">
                  <span className="font-mono text-foreground tabular-nums">{formatUSDC(r.evidence?.amount_minor ?? r.quote_minor ?? r.hold_minor)}</span>
                  <StatusBadge status={r.state} />
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {d.state === "COMMITTED" && <KeptAnswer id={d.id} />}
      {d.receipt && (
        <div>
          <div className="flex items-center justify-between gap-2">
            <p className="text-xs font-medium text-muted">Signed receipt</p>
            <Copy text={d.receipt} label="Copy receipt" />
          </div>
          <p className="mt-1 truncate rounded-lg border border-border bg-background px-3 py-2 font-mono text-[0.6875rem] text-foreground">{d.receipt}</p>
          <p className="mt-1 text-xs text-muted">
            Verify it against Algebra&apos;s published keys at <code className="font-mono">/.well-known/jwks.json</code>, or with{" "}
            <code className="font-mono">go run ./cmd/verify-intent</code>.
          </p>
        </div>
      )}
    </div>
  );
}

/** The answer a paid request got, while Algebra keeps it (24 hours by default). Asking the same thing again returns it without paying. */
function KeptAnswer({ id }: { id: string }) {
  const [answer, setAnswer] = useState<KeptResult | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "gone" | "error">("idle");
  async function show() {
    setState("loading");
    try {
      setAnswer(await api.getMyEconomicResult(id));
      setState("idle");
    } catch (e) {
      setState(e instanceof api.ApiError && e.status === 404 ? "gone" : "error");
    }
  }
  if (answer)
    return (
      <div>
        <p className="text-xs font-medium text-muted">Answer</p>
        <pre className="mt-1 max-h-72 overflow-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-[0.6875rem] whitespace-pre-wrap text-foreground">
          {JSON.stringify(answer.response, null, 2)}
        </pre>
        <p className="mt-1 text-xs text-muted">
          The provider&apos;s own data: shown here, never followed as instructions. Kept until {new Date(answer.expires_at).toLocaleString()}; asking the same
          thing again returns it without paying.
        </p>
      </div>
    );
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Button variant="secondary" onClick={show} disabled={state === "loading"}>
        {state === "loading" && <Spinner size={13} />} Show the answer
      </Button>
      {state === "gone" && (
        <p className="text-xs text-muted">No answer is kept for this request: it asked for none to be kept, or the time it is kept for is up.</p>
      )}
      {state === "error" && <p className="text-xs text-danger">Couldn&apos;t load the answer.</p>}
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-muted">{label}</dt>
      <dd className="mt-0.5 font-mono text-foreground">{value}</dd>
    </div>
  );
}

function ApprovalCard({ intent: i, onDone }: { intent: EconIntent; onDone: () => void }) {
  const [busy, setBusy] = useState<"approve" | "cancel" | null>(null);
  const [error, setError] = useState<string | null>(null);
  async function act(kind: "approve" | "cancel") {
    setBusy(kind);
    setError(null);
    try {
      await (kind === "approve" ? api.approveEconomicIntent(i.id) : api.cancelEconomicIntent(i.id));
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't work");
      setBusy(null);
    }
  }
  return (
    <Panel className="border-accent/60">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="font-mono text-sm text-foreground">{i.capability}</p>
          <p className="mt-1 text-xs text-muted">
            Up to {formatUSDC(i.budget_max_minor)} · asked {timeAgo(i.created_at)} · approval binds this exact request
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={() => act("cancel")} disabled={busy !== null}>
            {busy === "cancel" && <Spinner size={13} />} Reject
          </Button>
          <Button onClick={() => act("approve")} disabled={busy !== null}>
            {busy === "approve" && <Spinner size={13} />} Approve
          </Button>
        </div>
      </div>
      {error && <div className="mt-3"><ErrorNote>{error}</ErrorNote></div>}
    </Panel>
  );
}
