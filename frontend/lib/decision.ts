// The server's decision about one payment, in the words the console shows:
// ALLOW runs, HOLD waits for a person's yes, BLOCK pays nothing. The words
// and the reason codes only describe what the server decided; nothing here
// (or in the model that asked) can change it.

import type { Decision, Seller, Simulation, Verdict } from "./routing-types";

export function decisionOf(v: Verdict | Decision | string | undefined): Decision {
  if (v === "ALLOW") return "ALLOW";
  if (v === "REQUIRE_APPROVAL" || v === "HOLD") return "HOLD";
  return "BLOCK";
}

export const DECISION_WORDS: Record<Decision, { label: string; sentence: string }> = {
  ALLOW: { label: "ALLOW", sentence: "Within your rules: it runs without asking you." },
  HOLD: { label: "HOLD", sentence: "Waits for your yes. Nothing is paid until you approve." },
  BLOCK: { label: "BLOCK", sentence: "Refused. Nothing is paid, and nothing you or an agent says here can change that." },
};

const REASONS: Record<string, string> = {
  ASKED_FOR_APPROVAL: "you asked to approve every payment",
  PASS_FROZEN: "the pass is frozen by the kill switch",
  PASS_RATE_LIMITED: "the pass made as many paid calls this minute as it may",
  PASS_NEW_PROVIDER_NEEDS_APPROVAL: "a provider you've never paid needs your approval",
  PASS_NEW_PROVIDER_OVER_CAP: "a provider you've never paid asks more than your cap",
  PASS_UNVERIFIED_PROVIDER_NEEDS_APPROVAL: "the provider's identity isn't verified, and your pass asks you first",
  PASS_UNVERIFIED_PROVIDER_BLOCKED: "the provider's identity isn't verified, and your pass pays verified providers only",
  PASS_BUDGET_EXCEEDED: "over the pass's budget",
  PASS_PER_PURCHASE_LIMIT: "over the pass's per-call limit",
  PASS_APPROVAL_REQUIRED: "at or above your ask-me line",
  PASS_MERCHANT_NOT_ALLOWED: "the pass doesn't allow this provider",
  DEMO_SANDBOX_ONLY: "a demo account pays in the sandbox only",
  NETWORK_NOT_ALLOWED: "the call isn't allowed on that network",
  ENVIRONMENT_NETWORK_MISMATCH: "the network isn't one this project's environment may use",
  MAINNET_EXECUTION_DISABLED: "real-money mainnet payments are switched off on this server",
  PROVIDER_BLOCKED: "the provider is blocked",
  PAYEE_CHANGED: "the provider asked to be paid at a different address than the one pinned for it",
  no_provider_passes: "no provider passed every check",
  price_above_listing: "it asks more than its listing says",
  price_outlier: "its price is far above what this work usually costs",
  provider_down: "it is failing its health checks",
};

/** "PASS_PER_PURCHASE_LIMIT: …detail" or a bare code, as a sentence. */
export function reasonLine(raw: string): { code: string; text: string } {
  const code = raw.split(":")[0].trim();
  const detail = raw.includes(":") ? raw.slice(raw.indexOf(":") + 1).trim() : "";
  const known = REASONS[code];
  return { code, text: known ?? (detail || code.replace(/_/g, " ").toLowerCase()) };
}

/** The seller's identity and payment history, kept apart. */
export function sellerFacts(s: Seller | undefined): { identity: string; identityTone: "ok" | "warn" | "bad"; payment: string } {
  if (!s || !s.known) return { identity: "Not in Algebra's registry: identity unknown", identityTone: "warn", payment: "Not paid by Algebra before" };
  const payment = s.payment_tested ? "A paid call delivered once: the transaction worked. That is not identity." : "Not paid by Algebra yet";
  if (s.trust === "verified") return { identity: "Identity verified: every check passed and a person reviewed who runs it", identityTone: "ok", payment };
  if (s.trust === "blocked") return { identity: "Blocked", identityTone: "bad", payment };
  return { identity: "Identity not verified", identityTone: "warn", payment };
}

/** The provider that would be paid, or null when every one was refused: a refused provider is never "the one". */
export function leadCandidate(sim: Simulation) {
  return sim.would_pay ?? sim.candidates.find((c) => c.verdict !== "DENY") ?? null;
}

/** Why nothing would be paid, from the refused providers, when the dry run gave no overall reason. */
export function refusalReasons(sim: Simulation, max = 4): string[] {
  const seen = new Set<string>();
  for (const c of sim.candidates) {
    for (const r of c.reasons ?? []) {
      seen.add(r);
      if (seen.size >= max) return [...seen];
    }
  }
  return [...seen];
}
