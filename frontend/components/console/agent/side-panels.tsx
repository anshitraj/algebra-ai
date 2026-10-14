"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { EconIntent, RailStatus, SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { approvalRule } from "@/lib/pass-rules";
import { IconGauge, IconLock, IconShield, IconTag, IconWallet } from "@/components/icons";
import { ComingSoonChains, PoweredBySolana } from "@/components/chain-logos";
import { useNetwork } from "@/lib/network";
import { useSession } from "@/lib/session";
import { getOnchainPass, type OnchainPassView } from "@/lib/onchain-pass";

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

/** When the pass makes the person approve a call. Zero means every call, not "never". */
function approvalRow(pass: SpendPass): { value: string; hint: string } {
  const rule = approvalRule(pass);
  if (rule.kind === "always") return { value: "Every call", hint: "Each call waits for your tap" };
  if (rule.kind === "above") return { value: `≥ ${formatUSDC(rule.minor)}`, hint: "From this price, a call waits for your tap" };
  return { value: "Never", hint: "Calls within the limits run on their own" };
}

/** The pass this chat pays under, the wallet that pays, and what the chat has spent. */
export function PassPanel({
  pass,
  network,
  session,
}: {
  pass: SpendPass | null | undefined;
  /** Algebra's own payer: not shown here, so it is never mistaken for the person's wallet. */
  rail?: RailStatus;
  network: string;
  session: { spent: number; calls: number; steps: number };
}) {
  const sandbox = network === "sandbox";
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
          <Row icon={<IconShield />} label="Approval" {...approvalRow(pass)} />
          <Row
            icon={<IconShield />}
            label="Providers"
            value={pass.allowed_merchants.length ? String(pass.allowed_merchants.length) : "Any"}
            hint={pass.allowed_merchants.length ? pass.allowed_merchants.slice(0, 3).join(", ") : "Any provider in the catalogs"}
          />
        </ul>
      )}

      {sandbox ? (
        <>
          <PanelHeading>Sandbox</PanelHeading>
          <p className="border-y border-border px-5 py-3 text-xs leading-relaxed text-muted">
            Simulated providers and USDC. No wallet is used and nothing is written to a chain.
          </p>
        </>
      ) : (
        <>
          <PanelHeading>{`Your wallet · ${networkLabel(network)}`}</PanelHeading>
          <YourWallet />
          {pass && <VaultRow passId={pass.id} network={network} />}
        </>
      )}

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
      <div className="space-y-3 px-5 pt-5">
        <PoweredBySolana />
        <ComingSoonChains />
      </div>
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

const shortAddr = (a: string) => (a.length > 12 ? `${a.slice(0, 4)}…${a.slice(-4)}` : a);

/** The person's own wallet (the one Privy made at sign-in) and what it holds here. */
function YourWallet() {
  const { user } = useSession();
  const { wallets } = useNetwork();
  const mine = user?.wallets?.find((w) => w.chain === "solana" && w.kind === "embedded") ?? user?.wallets?.find((w) => w.chain === "solana");
  const held = mine ? wallets?.wallets.find((w) => w.address === mine.address) : undefined;
  return (
    <div className="flex gap-3 border-y border-border px-5 py-3">
      <span className="mt-0.5 text-muted">
        <IconWallet size={16} />
      </span>
      {!mine ? (
        <p className="text-xs leading-relaxed text-muted">No Solana wallet on your account yet. Sign in with Privy to get one.</p>
      ) : (
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline justify-between gap-3">
            <span className="font-mono text-xs text-foreground" title={mine.address}>
              {shortAddr(mine.address)}
            </span>
            <span className="shrink-0 font-mono text-sm text-primary tabular-nums">{held?.ok ? formatUSDC(held.usdc_minor) : "…"}</span>
          </div>
          <p className="mt-1 text-xs leading-snug text-muted">
            {held?.ok ? `${(held.sol_lamports / 1e9).toLocaleString("en-US", { maximumFractionDigits: 4 })} SOL for fees · ` : ""}
            {mine.kind === "embedded" ? "Privy wallet" : "your wallet"}. Hover it at the top right to fund it.
          </p>
        </div>
      )}
    </div>
  );
}

/** The pass's vault on Solana: the USDC locked in the program, which only the pass's limits can release. */
function VaultRow({ passId, network }: { passId: string; network: string }) {
  const [view, setView] = useState<OnchainPassView | null | undefined>(undefined);
  // A pass with no vault is read once, and again when the tab is looked at: polling a
  // vault that doesn't exist every 20 seconds only spent the session's request limit.
  // While a vault exists its balance moves with each payment, so that one is kept fresh.
  const hasVault = view?.exists === true;
  useEffect(() => {
    let live = true;
    const read = () =>
      getOnchainPass(passId)
        .then((v) => live && setView(v))
        .catch(() => live && setView((cur) => cur ?? null));
    void read();
    const onVisible = () => {
      if (document.visibilityState === "visible") void read();
    };
    document.addEventListener("visibilitychange", onVisible);
    const id = hasVault ? setInterval(() => void (document.visibilityState === "visible" && read()), 30_000) : undefined;
    return () => {
      live = false;
      document.removeEventListener("visibilitychange", onVisible);
      if (id) clearInterval(id);
    };
  }, [passId, hasVault]);
  const here = view && view.binding.network === network;
  return (
    <>
      <PanelHeading>Solana vault</PanelHeading>
      <div className="flex gap-3 border-y border-border px-5 py-3">
        <span className="mt-0.5 text-muted">
          <IconLock size={16} />
        </span>
        {view === undefined ? (
          <div className="h-8 flex-1 animate-pulse rounded-lg bg-border/60" />
        ) : !here || !view.exists ? (
          <div className="min-w-0 flex-1">
            <p className="text-sm text-foreground">No vault yet</p>
            <p className="mt-0.5 text-xs leading-snug text-muted">
              Lock USDC from your wallet in a Solana program, so this pass can only spend it within its limits.{" "}
              <Link href="/console/passes" className="font-medium text-primary hover:underline">
                Create the vault
              </Link>
            </p>
          </div>
        ) : view.agent_changed ? (
          <div className="min-w-0 flex-1">
            <p className="text-sm text-foreground">Made for an earlier Algebra payer key</p>
            <p className="mt-0.5 text-xs leading-snug text-muted">
              This vault can&apos;t fund payments any more. Your USDC is safe: withdraw it and make a new vault.{" "}
              <Link href="/console/passes" className="font-medium text-primary hover:underline">
                Open the pass
              </Link>
            </p>
          </div>
        ) : (
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline justify-between gap-3">
              <a href={view.explorer} target="_blank" rel="noopener noreferrer" className="font-mono text-xs text-foreground hover:underline" title={view.binding.address}>
                {shortAddr(view.binding.address)}
              </a>
              <span className="shrink-0 font-mono text-sm text-primary tabular-nums">{formatUSDC(view.vault_minor ?? 0)}</span>
            </div>
            <p className="mt-1 text-xs leading-snug text-muted">
              {view.state?.revoked ? "Revoked on chain" : view.state?.frozen ? "Frozen on chain" : "Live on chain"} ·{" "}
              {formatUSDC(view.state?.spent ?? 0)} spent of {formatUSDC(view.state?.total_budget ?? 0)}
            </p>
          </div>
        )}
      </div>
    </>
  );
}
