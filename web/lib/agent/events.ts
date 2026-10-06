// Wire format of /api/agent/chat's streamed response: one JSON object per
// line (NDJSON). The route emits these as the provider loop runs, so the UI
// can show each tool call as a live step instead of a spinner.

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
  | { type: "approval"; intentId: string; provider?: string }
  /** The agent asked 1-4 multiple-choice questions; the turn ends and the picks come back as the next message. */
  | { type: "question"; questions: AskedQuestion[] }
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
};

export type AskedQuestion = { question: string; options: string[] };

export type EmitFn = (e: AgentEvent) => void;
