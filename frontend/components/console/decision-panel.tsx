"use client";

// What a person sees before a paid call: who would be paid, the estimated total,
// whether the seller's identity is verified (apart from whether a payment ever
// worked), the network, the Spend Pass's rules, and the server's decision as
// ALLOW, HOLD or BLOCK with its reason codes. It describes the server's answer;
// it can't change it, and nothing the agent says appears here as fact.

import type { Simulation } from "@/lib/routing-types";
import type { SpendPass } from "@/lib/types";
import { DECISION_WORDS, decisionOf, leadCandidate, reasonLine, refusalReasons, sellerFacts } from "@/lib/decision";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { pickLabel } from "@/lib/strategy";
import { policyReport, type PolicyReport } from "@/lib/policy-check";
import { IconBan, IconCheck, IconClock, IconShield } from "@/components/icons";

const TONE = {
  ALLOW: { ring: "border-primary/40", chip: "bg-primary text-primary-tint", icon: <IconCheck size={13} strokeWidth={3} /> },
  HOLD: { ring: "border-accent/60", chip: "bg-accent text-accent-tint", icon: <IconClock size={13} /> },
  BLOCK: { ring: "border-danger/50", chip: "bg-danger text-danger-tint", icon: <IconBan size={13} /> },
};

export function DecisionPanel({ sim, pass, rules: given, compact = false }: { sim: Simulation; pass?: SpendPass | null; rules?: PolicyReport | null; compact?: boolean }) {
  const decision = decisionOf(sim.decision ?? sim.verdict);
  const tone = TONE[decision];
  const lead = leadCandidate(sim);
  const seller = sellerFacts(lead?.seller);
  const network = lead?.network;
  const test = !!network && network !== "solana";
  const reasons = (sim.reasons?.length ? sim.reasons : lead ? (lead.reasons ?? []) : refusalReasons(sim)).map(reasonLine);
  const base = given ?? (pass ? policyReport(pass, lead?.total_minor, false, true) : null);
  // The approval line reads differently once the server has decided: a HOLD is waiting, a BLOCK never got that far.
  const rules = base && {
    ...base,
    checks: base.checks.map((c) =>
      c.label === "Ask me above" && decision !== "ALLOW"
        ? { label: "Your approval", result: decision === "HOLD" ? "this payment waits for your yes" : "not reached: it is blocked first", state: "asked" as const }
        : c,
    ),
  };

  return (
    <div className={`rounded-2xl border ${tone.ring} bg-surface`} data-decision={decision}>
      <div className="flex flex-wrap items-center gap-2.5 border-b border-border px-4 py-3">
        <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold tracking-wide ${tone.chip}`}>
          {tone.icon} {DECISION_WORDS[decision].label}
        </span>
        <span className="text-sm text-foreground">{DECISION_WORDS[decision].sentence}</span>
      </div>

      <dl className={`grid gap-x-6 gap-y-2.5 px-4 py-3 text-sm ${compact ? "" : "sm:grid-cols-2"}`}>
        <Row label="Provider" value={lead ? lead.provider : "None passed every check"} mono />
        <Row
          label="Estimated total"
          value={lead ? `${formatUSDC(lead.total_minor)}${lead.price_source === "live" ? " · live price" : " · listed price, not confirmed"}` : "—"}
        />
        <Row label="Network" value={network ? `${networkLabel(network)}${test ? " · test money" : " · real funds"}` : "—"} />
        <Row label="Picked by" value={sim.strategy ? pickLabel(sim.strategy) || sim.strategy : "—"} />
        <div className="sm:col-span-2">
          <dt className="text-xs text-muted">Seller</dt>
          <dd className="mt-0.5 space-y-0.5">
            <p className={`flex items-center gap-1.5 ${seller.identityTone === "ok" ? "text-primary" : seller.identityTone === "bad" ? "text-danger" : "text-foreground"}`}>
              {seller.identityTone === "ok" ? <IconCheck size={13} strokeWidth={3} /> : seller.identityTone === "bad" ? <IconBan size={13} /> : <IconShield size={13} className="text-muted" />}
              {seller.identity}
            </p>
            <p className="text-xs text-muted">{seller.payment}</p>
          </dd>
        </div>
      </dl>

      {rules && (
        <div className="border-t border-border px-4 py-3">
          <p className="text-xs font-medium text-muted">Spending policy · {rules.pass}</p>
          <ul className="mt-1.5 grid gap-x-6 gap-y-1 text-xs sm:grid-cols-2">
            {rules.checks.map((c) => (
              <li key={c.label}>
                <span className="text-muted">{c.label}: </span>
                <span className={c.state === "open" ? "text-muted italic" : "text-foreground"}>{c.result}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {reasons.length > 0 && (
        <div className="border-t border-border px-4 py-3">
          <p className="text-xs font-medium text-muted">Why</p>
          <ul className="mt-1.5 space-y-1">
            {reasons.map((r, i) => (
              <li key={`${r.code}-${i}`} className="flex flex-wrap items-baseline gap-x-2 text-sm text-foreground">
                <code className="rounded bg-primary-tint px-1.5 py-0.5 font-mono text-[0.7rem] text-foreground">{r.code}</code>
                <span className="text-muted">{r.text}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className={`mt-0.5 truncate text-foreground ${mono ? "font-mono text-[0.8rem]" : ""}`}>{value}</dd>
    </div>
  );
}
