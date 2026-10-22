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
  PASS_WEB_FOUND_NEEDS_APPROVAL: "this endpoint was found on the open web, not in a catalog, and your pass asks you first",
  PASS_WEB_FOUND_BLOCKED: "this endpoint was found on the open web, not in a catalog, and your pass doesn't pay those",
  PASS_BUDGET_EXCEEDED: "over the pass's budget",
  PASS_PER_PURCHASE_LIMIT: "over the pass's per-call limit",
  PASS_APPROVAL_REQUIRED: "at or above your ask-me line",
  PASS_MERCHANT_NOT_ALLOWED: "the pass doesn't allow this provider",
  DEMO_SANDBOX_ONLY: "a demo account pays in the sandbox only",
  NETWORK_NOT_ALLOWED: "the call isn't allowed on that network",
  ENVIRONMENT_NETWORK_MISMATCH: "the network isn't one this project's environment may use",
  MAINNET_EXECUTION_DISABLED: "real-money mainnet payments are switched off on this server",
  TEST_MONEY_ACCOUNT_LIMIT: "you've used today's test-money payments on Devnet; they come back within 24 hours",
  TEST_MONEY_DAY_LIMIT: "Algebra's shared test wallet has made all its payments for today; try again later",
  ONCHAIN_PASS_REQUIRED: "payments on this network come from a Spend Pass vault, and this pass has none",
  ONCHAIN_PASS_MISSING: "the pass's vault is gone: its owner closed it on Solana",
  ONCHAIN_PASS_OTHER_NETWORK: "the pass's vault is on another network",
  ONCHAIN_PASS_FROZEN: "the pass is frozen on Solana by its owner",
  ONCHAIN_PASS_REVOKED: "the pass was revoked on Solana by its owner",
  ONCHAIN_PASS_EXPIRED: "the pass has expired on Solana",
  ONCHAIN_PASS_LIMIT: "over a limit the pass enforces on Solana",
  ONCHAIN_PASS_UNFUNDED: "the vault holds less than this costs",
  ONCHAIN_PASS_AGENT_CHANGED: "the vault was made for an earlier Algebra payer key, so it can't fund payments any more",
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

/** What a person does about a refusal that comes from the pass's vault: the sentence, the action and where it is done. */
export function vaultFix(reasons: string[], passId?: string): { text: string; action: string; href: string } | null {
  const codes = reasons.map((r) => r.split(":")[0].trim());
  const has = (...c: string[]) => codes.find((x) => c.includes(x));
  // The Spend firewall page, scrolled to this pass when it is known.
  const href = passId ? `/console/firewall#pass-${encodeURIComponent(passId)}` : "/console/firewall";
  if (has("ONCHAIN_PASS_REQUIRED", "ONCHAIN_PASS_MISSING", "ONCHAIN_PASS_OTHER_NETWORK")) {
    return { text: "Payments on this network come from a Spend Pass vault, and this pass has none yet.", action: "Create the vault", href };
  }
  if (has("ONCHAIN_PASS_UNFUNDED")) return { text: "The vault holds less than this costs.", action: "Add USDC to the vault", href };
  if (has("ONCHAIN_PASS_AGENT_CHANGED")) {
    return { text: "This vault was made for an earlier Algebra payer key and can't fund payments. Your USDC is safe: withdraw it and make a new vault.", action: "Open the pass", href };
  }
  if (has("ONCHAIN_PASS_FROZEN", "ONCHAIN_PASS_REVOKED", "ONCHAIN_PASS_EXPIRED")) {
    return { text: "The pass's vault can't pay right now.", action: "Open the pass", href };
  }
  return null;
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
