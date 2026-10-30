"use client";

import Link from "next/link";
import { useState } from "react";
import type { OptionGroup } from "@/lib/agent/events";
import type { RailStatus, SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { IconArrowRight, IconChevronDown, IconGrid, IconShield, IconWallet } from "@/components/icons";
import { PassPanel, RecentCallsPanel } from "./side-panels";

export function RequestPanel({ request, groups, pass, network, session, rail, callsKey, onEdit, onChangePass, children }: {
  request?: string;
  groups: OptionGroup[];
  pass: SpendPass | null | undefined;
  network: string;
  session: { spent: number; calls: number; steps: number };
  rail?: RailStatus;
  callsKey: number;
  onEdit: () => void;
  onChangePass: () => void;
  children?: React.ReactNode;
}) {
  const [walletOpen, setWalletOpen] = useState(false);
  const [recentOpen, setRecentOpen] = useState(false);
  const limits = [...new Set(groups.map((g) => g.maxPriceUsdc))];
  const spent = pass ? Math.max(0, pass.budget_minor_units - pass.remaining_minor_units) : 0;
  const percent = pass && pass.budget_minor_units > 0 ? Math.min(100, spent / pass.budget_minor_units * 100) : 0;
  return (
    <div className="request-panel">
      <section>
        <div className="request-panel-heading"><h2>Current request</h2>{request && <button type="button" onClick={onEdit}>Edit</button>}</div>
        <p className="request-preview">{request || "Your next request starts here. Tell your agent what you need and your budget."}</p>
        <dl className="request-facts">
          <div><dt><span className="request-solana" aria-hidden="true">≋</span>Network</dt><dd>{network === "sandbox" ? "Demo" : `Solana ${networkLabel(network)}`}<span className="request-status-dot" /></dd></div>
          <div><dt><IconShield size={15} />Max cost</dt><dd>{limits.length === 1 ? `${limits[0]} USDC / task` : limits.length > 1 ? "Set per task" : "Set in your request"}</dd></div>
          {groups.length > 0 && <div><dt><IconGrid size={15} />Tools</dt><dd>{[...new Set(groups.map((g) => g.title))].join(", ")}</dd></div>}
        </dl>
      </section>
      <section>
        <div className="request-panel-heading"><h2>Spend pass</h2><button type="button" onClick={onChangePass}>Change</button></div>
        {pass === undefined ? <p className="text-xs text-muted" aria-busy="true">Loading your pass…</p> : pass === null ? <Link className="request-empty-pass" href="/console/passes">Choose a Spend Pass to pay <IconArrowRight size={14} /></Link> : (
          <div className="request-pass-card">
            <div><IconWallet size={16} /><span title={pass.label}>{pass.label}</span><strong>{formatUSDC(pass.remaining_minor_units)}</strong></div>
            <div className="request-budget-track" role="meter" aria-label="Spend Pass budget used" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${percent}%` }} /></div>
            <p>{formatUSDC(pass.remaining_minor_units)} left of {formatUSDC(pass.budget_minor_units)}</p>
          </div>
        )}
      </section>
      <details className="request-disclosure">
        <summary>Advanced <IconChevronDown size={14} /></summary>
        <div className="request-advanced">{children}<Link href="/console/firewall"><IconShield size={15} />Provider & spending rules <IconArrowRight size={13} /></Link></div>
      </details>
      <details className="request-disclosure" onToggle={(e) => setWalletOpen(e.currentTarget.open)}>
        <summary>Pass & wallet details <IconChevronDown size={14} /></summary>
        {walletOpen && <PassPanel pass={pass} rail={rail} network={network} session={session} />}
      </details>
      <details className="request-disclosure" onToggle={(e) => setRecentOpen(e.currentTarget.open)}>
        <summary>Recent calls <IconChevronDown size={14} /><span className="sr-only">{session.calls} calls in this chat</span></summary>
        {recentOpen && <RecentCallsPanel refreshKey={callsKey} />}
      </details>
      <Link href="/console/providers" className="request-providers-link">View all providers <IconArrowRight size={14} /></Link>
    </div>
  );
}
