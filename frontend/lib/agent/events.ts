// Wire format of /api/agent/chat's streamed response: one JSON object per
// line (NDJSON). The route emits these as the provider loop runs, so the UI
// can show each tool call as a live step instead of a spinner.

import type { Decision, Recommendation, Simulation } from "../routing-types";
import type { ProviderTrust } from "../types";

export type StepStatus = "running" | "done" | "error" | "blocked" | "waiting";

export type AgentEvent =
  | { type: "step"; id: string; tool: string; status: "running"; title: string; hint?: StepHint }
  | {
      type: "step";
      id: string;
      tool: string;
      status: Exclude<StepStatus, "running">;
      title: string;
      summary?: string;
      detail?: StepDetail;
    }
  | { type: "text"; text: string }
  /** A paid call is waiting for the person: approve or cancel it in the card. */
  | { type: "approval"; intentId: string; provider?: string; askedFirst?: boolean; decision?: Simulation }
  /** The agent asked 1-4 multiple-choice questions; the turn ends and the picks come back as the next message. */
  | { type: "question"; questions: AskedQuestion[] }
  /** Priced offers for one piece of work, as cards the person chooses from. Nothing is paid until they choose. */
  | { type: "options"; group: OptionGroup }
  /** A paid call's receipt: what was paid, where, the transaction and the PDF. */
  | { type: "receipt"; receipt: ReceiptInfo }
  /** USDC paid for a delivered call (micro-USDC). */
  | { type: "spend"; amount: { minor_units: number; currency: string }; intentId: string; provider: string; network?: string }
  | { type: "done"; reply: string; history: unknown[]; provider: string; model: string }
  | { type: "error"; error: string };

/** What a running step is working on, for its live activity panel. */
export type StepHint = { query?: string; budget?: string; provider?: string; network?: string };

/** A provider as a step card shows it. */
export type ProviderCard = {
  id: string;
  name: string;
  description?: string;
  catalog: string;
  price?: string;
  endpoints?: number;
  networks?: string[];
  host?: string;
  website?: string;
  logo?: string;
  fqn?: string;
  url?: string;
  /** What Algebra's registry knows about its endpoints. Absent: no record. */
  trust?: ProviderTrust;
};

/** Structured bits of a tool result worth rendering (never raw JSON dumps). */
export type StepDetail = {
  rows?: { label: string; value: string }[];
  providers?: ProviderCard[];
  endpoints?: { capability: string; method: string; path: string; description?: string; price?: string; callable: boolean }[];
  /** The provider's own response, pretty-printed and bounded: untrusted data. */
  response?: string;
  links?: { title: string; url: string }[];
  reasons?: string[];
  /** One line of context shown under the detail. */
  note?: string;
  /** What the Spend Pass's rules said about this payment. */
  policy?: import("../policy-check").PolicyReport;
  /** The server's dry-run decision: provider, total, seller, network, rules, ALLOW / HOLD / BLOCK. */
  decision?: Simulation;
};

export type AskedQuestion = { question: string; options: string[] };

/**
 * One comparison the agent asked for: every provider of a piece of work priced
 * live (unpaid), and the best offer each way. The person picks one or more
 * cards and pays with a click; the model never pays.
 */
export type OptionGroup = {
  /** Stable for this comparison: a card's payment is named after it, so a double click pays once. */
  id: string;
  /** A class of work ("token.price") or, for a named provider, its endpoint's capability. */
  work: string;
  title: string;
  input: Record<string, unknown>;
  maxPriceUsdc: number;
  /** Set when the comparison was for one named provider. */
  providerId?: string;
  network: string;
  decision: Decision;
  reasons?: string[];
  recommendations: Recommendation[];
  /** Every offer, ranked balanced; at most 10. */
  compared: Recommendation[];
  /** Providers left out, and why (down, overcharging, blocked, not allowed by the pass). */
  refused: { provider: string; reason: string }[];
};

/** A card the person chose to pay: sent back with the next chat request. */
export type Choice = {
  group: string;
  work: string;
  input: Record<string, unknown>;
  max_price_usdc: number;
  provider: string;
  strategy?: "cheapest" | "balanced" | "fastest";
  label: string;
};

/** A delivered, paid call as the receipt card shows it. */
export type ReceiptInfo = {
  intentId: string;
  provider: string;
  paid: string;
  paidMinor: number;
  network?: string;
  transaction?: string;
  explorerUrl?: string;
  solscanUrl?: string;
  /** The PDF receipt, generated from the signed receipt. */
  pdfUrl: string;
  /** The receipt as a page, with Algebra's signature checked. */
  pageUrl: string;
  test?: boolean;
  at: string;
  /** The delivered provider response, kept as data for the result cards. */
  result?: unknown;
  work?: string;
  stepId?: string;
  elapsedMs?: number;
};

export type EmitFn = (e: AgentEvent) => void;
