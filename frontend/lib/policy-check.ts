// What a Spend Pass's rules said about one payment, in words: which rules it
// met, which ones aren't set at all, and whether a person was asked. Shown on
// the chat's step card and on the invoice, so "it passed" never hides "there
// was nothing to pass".

import type { SpendPass } from "./types";

/** pass: a rule that applied and was met. open: no rule is set here. asked: a person said yes. */
export type PolicyCheckState = "pass" | "open" | "asked";

export type PolicyCheck = { label: string; result: string; state: PolicyCheckState };

export type PolicyReport = {
  pass: string;
  /** One line: how this payment was allowed. */
  headline: string;
  /** False when the pass has no rules of the person's own beyond its budget: the defaults decided. */
  custom: boolean;
  checks: PolicyCheck[];
};

// The controls every pass gets unless a person changes them (backend/internal/domain/spendpass/controls.go).
const DEFAULT_PER_MINUTE = 60;
const DEFAULT_PER_PROVIDER = 20;

const usdc = (minor: number) => `${(minor / 1_000_000).toLocaleString("en-US", { maximumFractionDigits: 6 })} USDC`;

/** Whether the person set any rule of their own on the pass, beyond its budget and expiry. */
export function hasOwnRules(p: SpendPass): boolean {
  const c = p.controls;
  return (
    p.approve_above_minor_units != null ||
    p.max_per_purchase_minor_units != null ||
    (p.allowed_merchants?.length ?? 0) > 0 ||
    (!!c &&
      ((c.max_calls_per_minute > 0 && c.max_calls_per_minute !== DEFAULT_PER_MINUTE) ||
        (c.max_calls_per_provider_per_minute > 0 && c.max_calls_per_provider_per_minute !== DEFAULT_PER_PROVIDER) ||
        c.new_providers === "approve" ||
        // "approve" is what a web find gets when nothing is set: only a change is the person's own rule.
        (!!c.web_found_providers && c.web_found_providers !== "approve")))
  );
}

/**
 * The pass's rules against one payment of paidMinor (0 or undefined when
 * nothing was paid yet). askedPerson: a person approved this payment.
 *
 * basis says where p.remaining_minor_units stands: "before" the payment (the
 * pass as read when the turn began; spentBefore is what earlier payments in
 * that same turn already took), or "now" (the pass as read afterwards, which
 * already counts this payment, as on an invoice: the payment must not be taken
 * off a second time).
 */
export function policyReport(
  p: SpendPass,
  paidMinor: number | undefined,
  askedPerson: boolean,
  pending = false,
  basis: "before" | "now" = "before",
  spentBefore = 0,
): PolicyReport {
  const paid = paidMinor ?? 0;
  const checks: PolicyCheck[] = [];
  const left = Math.max(0, p.remaining_minor_units - spentBefore - paid);
  checks.push({
    label: "Budget",
    result: pending
      ? `${usdc(p.remaining_minor_units)} left of ${usdc(p.budget_minor_units)}; this call would use ${usdc(paid)}`
      : basis === "now"
        ? `${usdc(p.remaining_minor_units)} left of ${usdc(p.budget_minor_units)} now`
        : `${usdc(left)} left of ${usdc(p.budget_minor_units)}${paid > 0 ? " after this call" : ""}`,
    state: "pass",
  });
  checks.push(
    p.max_per_purchase_minor_units != null
      ? { label: "Cap per call", result: `${pending ? "would cost" : "paid"} ${usdc(paid)}, cap ${usdc(p.max_per_purchase_minor_units)}`, state: "pass" }
      : { label: "Cap per call", result: "none set: any single call up to the budget", state: "open" },
  );
  if (askedPerson) {
    checks.push({ label: "Your approval", result: "you said yes to this payment", state: "asked" });
  } else if (p.approve_above_minor_units != null) {
    checks.push({
      label: "Ask me above",
      result: `${usdc(p.approve_above_minor_units)}: this call was below it, so you weren't asked`,
      state: "pass",
    });
  } else {
    checks.push({ label: "Ask me above", result: "none set: nothing waits for your yes", state: "open" });
  }
  checks.push(
    (p.allowed_merchants?.length ?? 0) > 0
      ? { label: "Providers", result: `only ${p.allowed_merchants.length} you picked`, state: "pass" }
      : { label: "Providers", result: "any provider (no allow-list)", state: "open" },
  );
  const c = p.controls;
  checks.push({
    label: "Speed limit",
    result: `${c?.max_calls_per_minute || DEFAULT_PER_MINUTE} paid calls a minute, ${c?.max_calls_per_provider_per_minute || DEFAULT_PER_PROVIDER} per provider${!c || c.max_calls_per_minute === DEFAULT_PER_MINUTE ? " (default)" : ""}`,
    state: "pass",
  });
  const custom = hasOwnRules(p);
  const headline = askedPerson
    ? "Paid after you approved it"
    : custom
      ? `Allowed by your rules on "${p.label}", without asking you`
      : `Paid without any policy of yours: only "${p.label}"'s budget and Algebra's defaults applied`;
  return { pass: p.label, headline, custom, checks };
}
