// Turns a raw tool call + result into a human step for the UI trace:
// "Paying Birdeye → Delivered · 0.003 USDC on mainnet". Pure, server-side,
// and defensive — tool results are whatever the API returned.

import type { ProviderCard, StepDetail, StepHint, StepStatus } from "./events";
import type { ToolResult } from "./execute-tool";
import { explorerTx, networkWord } from "../explorer";

const USDC = 1_000_000;

function usdc(minor: unknown): string | undefined {
  return typeof minor === "number" ? `${(minor / USDC).toLocaleString("en-US", { maximumFractionDigits: 6 })} USDC` : undefined;
}

const CATALOGS: Record<string, string> = { "pay.sh": "Pay.sh", circle: "Circle Agent Marketplace", payai: "PayAI" };

const REASONS: Record<string, string> = {
  PASS_APPROVAL_REQUIRED: "At or above the pass's approval line",
  PASS_BUDGET_EXCEEDED: "Would exceed what's left of the pass's budget",
  PASS_PER_PURCHASE_LIMIT: "Over the pass's per-call cap",
  PASS_MERCHANT_NOT_ALLOWED: "Provider isn't on the pass's list",
  PASS_CATEGORY_NOT_ALLOWED: "Category isn't allowed by the pass",
  PASS_CURRENCY_MISMATCH: "The pass isn't in USDC",
  PASS_EXPIRED: "The pass has expired",
  PASS_REVOKED: "The pass was revoked",
  CATEGORY_BLOCKED: "Category is blocked",
  quote_over_budget: "The endpoint asked for more than the limit",
  provider_not_allowed: "Provider isn't on the pass's list",
  authority_denied: "The Spend Pass doesn't allow it",
  already_committed: "Already paid for once; a repeat is blocked",
  frozen_unknown_outcome: "A payment's outcome is still being confirmed",
  intent_closed: "This call was cancelled or has expired",
};

export function reasonText(code: string) {
  return REASONS[code] ?? code.replace(/_/g, " ").toLowerCase();
}

const TITLES: Record<string, string> = {
  search_providers: "Searching the catalogs",
  get_provider_endpoints: "Reading the endpoints",
  pay_and_call: "Paying for the call",
  run_approved_intent: "Running the approved call",
  execution_status: "Checking the payment",
};

function shortId(id: string) {
  const name = id.replace(/^(paysh|circle|payai):/, "");
  return name.length > 40 ? `${name.slice(0, 39)}…` : name;
}

export function stepTitle(tool: string, input: Record<string, unknown>): string {
  if (tool === "search_providers" && typeof input.query === "string" && input.query) return `${TITLES[tool]} for “${input.query}”`;
  if ((tool === "get_provider_endpoints" || tool === "pay_and_call") && typeof input.provider_id === "string") {
    return tool === "pay_and_call" ? `Paying ${shortId(input.provider_id)}` : `Reading ${shortId(input.provider_id)}'s endpoints`;
  }
  return TITLES[tool] ?? tool.replace(/_/g, " ");
}

/** What a running step is working with, shown while it runs. */
export function stepHint(tool: string, input: Record<string, unknown>, network?: string): StepHint | undefined {
  const query = typeof input.query === "string" && input.query ? input.query : undefined;
  const max = input.max_price_usdc;
  const budget = typeof max === "number" && max > 0 ? `${max} USDC` : undefined;
  const provider = typeof input.provider_id === "string" ? shortId(input.provider_id) : undefined;
  return query || budget || provider ? { query, budget, provider, network } : { network };
}

export type StepOutcome = { status: Exclude<StepStatus, "running">; summary?: string; detail?: StepDetail };

function pretty(v: unknown): string | undefined {
  if (v === undefined || v === null) return undefined;
  const text = typeof v === "string" ? v : JSON.stringify(v, null, 2);
  return text.length > 4_000 ? `${text.slice(0, 4_000)}\n…` : text;
}

/** The rail's reasons, in words a person can act on. Anything else is shown as the backend said it. */
export function friendlyFailure(message: string): string {
  const net = (n: string | undefined) => (n === "solana-devnet" ? "devnet" : n === "solana" ? "mainnet" : (n ?? "this network"));
  const none = message.match(/has no USDC account on (S+)/);
  if (none) {
    const n = net(none[1]);
    return `The ${n} wallet has no USDC yet, so nothing was paid.${none[1] === "solana-devnet" ? " Add test USDC at faucet.circle.com (Solana Devnet)." : ""}`;
  }
  if (/insufficient USDC/.test(message)) return "The wallet doesn't hold enough USDC for this call, so nothing was paid.";
  const payee = message.match(/provider's USDC account S+ doesn't exist on (S+)/);
  if (payee) return `The provider's payout account doesn't exist on ${net(payee[1])}, so it can't be paid. Nothing was paid.`;
  return message.replace(/^payment authority refused: /, "");
}

function paidOutcome(d: Record<string, unknown>): StepOutcome {
  const outcome = String(d.outcome ?? "");
  const network = typeof d.network === "string" ? d.network : undefined;
  const tx = typeof d.transaction === "string" && d.transaction ? d.transaction : undefined;
  const rows: { label: string; value: string }[] = [];
  if (typeof d.paid === "string" && (d.paid_minor as number) > 0) rows.push({ label: "Paid", value: `${d.paid}${network ? ` on ${networkWord(network)}` : ""}` });
  if (tx) rows.push({ label: "Transaction", value: `${tx.slice(0, 8)}…${tx.slice(-8)}` });
  if (typeof d.intent_id === "string") rows.push({ label: "Intent", value: d.intent_id });
  const links = [
    ...(tx && !d.test_money ? [{ title: "Transaction on Solana Explorer", url: explorerTx(tx, network) }] : []),
    ...(tx && d.test_money ? [{ title: "Transaction on Solana Explorer (devnet)", url: explorerTx(tx, network) }] : []),
    { title: "Every paid call, with its receipt", url: "/console/executions" },
  ];

  switch (outcome) {
    case "approval_required":
      return {
        status: "waiting",
        summary: `Waiting for your approval${typeof d.budget === "string" ? ` · up to ${d.budget}` : ""}`,
        detail: { reasons: ["The Spend Pass asks you to approve calls at this price. Nothing has been paid."] },
      };
    case "refused": {
      const codes = Array.isArray(d.reason_codes) ? (d.reason_codes as string[]) : [];
      const rejected = Array.isArray(d.rejected) ? (d.rejected as { code?: string; detail?: string; provider?: string }[]) : [];
      const reasons = [
        ...codes.filter((c) => !c.endsWith("_OK")).map(reasonText),
        ...rejected.map((r) => {
          const why = r.detail && !/^[A-Z_]+$/.test(r.detail) ? r.detail : reasonText(r.detail || r.code || "");
          return [r.provider ? shortId(r.provider) : "", why].filter(Boolean).join(": ");
        }),
      ];
      return {
        status: "blocked",
        summary: reasons.length ? `Refused before paying: ${reasons[0].replace(/^[^:]+: /, "")}` : typeof d.reason === "string" ? d.reason.replace(/^app: /, "") : "Refused before paying",
        detail: { reasons: reasons.length ? reasons : ["Nothing was paid."] },
      };
    }
    case "delivered":
      return {
        status: "done",
        summary: `Delivered${typeof d.paid === "string" && (d.paid_minor as number) > 0 ? ` · ${d.paid}` : " · nothing to pay"}${network ? ` on ${networkWord(network)}` : ""}${d.test_money ? " (test money)" : ""}`,
        detail: { rows, links, response: pretty(d.response), note: "The response is the provider's own data, shown as received." },
      };
    case "being_confirmed":
      return {
        status: "waiting",
        summary: "Paid; confirming the outcome on-chain",
        detail: { rows, links, note: "Algebra won't pay again while it confirms what happened." },
      };
    default: {
      const failure = d.failure as { class?: string; message?: string } | undefined;
      return {
        status: "error",
        summary: friendlyFailure(failure?.message || (typeof d.summary === "string" ? d.summary : "The call failed")),
        detail: { rows, links: links.slice(-1), response: pretty(d.response), note: (d.paid_minor as number) > 0 ? undefined : "Nothing was paid." },
      };
    }
  }
}

export function summarizeStep(tool: string, input: Record<string, unknown>, result: ToolResult): StepOutcome {
  if (!result.ok) {
    const notConfigured = /not implemented|not enabled|not configured/i.test(result.error);
    return { status: "error", summary: notConfigured ? "Not available on this server" : result.error };
  }
  const data = (result.data ?? {}) as Record<string, unknown>;

  switch (tool) {
    case "search_providers": {
      const list = (data.providers as Record<string, unknown>[] | undefined) ?? [];
      const providers: ProviderCard[] = list.map((p) => ({
        id: String(p.id),
        name: String(p.name ?? p.id),
        description: typeof p.description === "string" ? p.description : undefined,
        catalog: CATALOGS[String(p.catalog)] ?? String(p.catalog ?? ""),
        price: typeof p.listed_price === "string" ? p.listed_price : undefined,
        endpoints: typeof p.endpoints === "number" ? p.endpoints : undefined,
        networks: Array.isArray(p.networks) ? (p.networks as string[]) : undefined,
        host: typeof p.host === "string" ? p.host : undefined,
        website: typeof p.website === "string" ? p.website : undefined,
        logo: typeof p.logo_url === "string" ? p.logo_url : undefined,
        fqn: typeof p.fqn === "string" ? p.fqn : undefined,
        url: typeof p.page_url === "string" ? p.page_url : undefined,
      }));
      const total = typeof data.total === "number" ? data.total : providers.length;
      return {
        status: "done",
        summary: providers.length
          ? `${total} provider${total === 1 ? "" : "s"} on ${networkWord(data.network)}${total > providers.length ? `, showing ${providers.length}` : ""}`
          : `Nothing payable on ${networkWord(data.network)} matches`,
        detail: providers.length ? { providers, note: "Listed prices, not quotes: Algebra asks for the real price before it pays." } : undefined,
      };
    }
    case "get_provider_endpoints": {
      const eps = (data.endpoints as Record<string, unknown>[] | undefined) ?? [];
      const callable = eps.filter((e) => e.callable).length;
      return {
        status: "done",
        summary: `${data.endpoint_count ?? eps.length} endpoint${data.endpoint_count === 1 ? "" : "s"}, ${callable} of those shown callable on ${networkWord(data.network)}`,
        detail: eps.length
          ? {
              endpoints: eps.slice(0, 12).map((e) => ({
                capability: String(e.capability),
                method: String(e.method ?? ""),
                path: String(e.path ?? ""),
                description: typeof e.description === "string" ? e.description : undefined,
                price: typeof e.listed_price === "string" ? e.listed_price : undefined,
                callable: !!e.callable,
              })),
            }
          : undefined,
      };
    }
    case "pay_and_call":
    case "run_approved_intent":
      return paidOutcome(data);
    case "execution_status": {
      const tx = typeof data.transaction === "string" ? data.transaction : undefined;
      return {
        status: "done",
        summary: [String(data.state ?? "").toLowerCase().replace(/_/g, " "), typeof data.committed === "string" ? `${data.committed} committed` : ""].filter(Boolean).join(" · "),
        detail: tx ? { links: [{ title: "Transaction on Solana Explorer", url: explorerTx(tx, typeof data.network === "string" ? data.network : undefined) }] } : undefined,
      };
    }
    default:
      return { status: "done" };
  }
}

export { usdc as formatUsdcMinor };
