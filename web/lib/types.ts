// Hand-written against the real Go domain structs and REST handlers
// (internal/domain/*, internal/api/v1/*) — openapi/v1.yaml's schemas are
// thinner than what the API actually returns for several endpoints
// (quotes/orders serialize the full domain struct), so these types are the
// ground truth, not the generated lib/api-types.ts.

// --- Spend Passes (internal/domain/spendpass, /api/v1/me/passes) ---

export type AgentKind = "claude" | "chatgpt" | "custom";
export type BudgetPeriod = "total" | "week" | "month";

export type SpendPass = {
  id: string;
  agent_id: string;
  label: string;
  agent_kind: AgentKind;
  currency: string;
  budget_minor_units: number;
  budget_period: BudgetPeriod;
  max_per_purchase_minor_units?: number;
  approve_above_minor_units?: number;
  allowed_categories: string[];
  allowed_merchants: string[];
  created_at: string;
  expires_at: string;
  revoked_at?: string;
  active: boolean;
  spent_minor_units: number;
  remaining_minor_units: number;
  window_starts_at: string;
  /** Velocity limits and the new-provider rule (see lib/routing-types). */
  controls?: import("./routing-types").PassControls;
  /** Set while the kill switch is on for this pass. */
  frozen_at?: string;
};

export type PassConnect = { api_base: string; mcp_url?: string };

/** A new pass with its agent token — the token is shown exactly once. */
export type IssuedPass = SpendPass & { token: string; connect: PassConnect };

export type NewPass = {
  label: string;
  agent_kind: AgentKind;
  /** "INR" (the default) or "USDC". Amounts are minor units of it. */
  currency?: string;
  budget_minor_units: number;
  budget_period: BudgetPeriod;
  max_per_purchase_minor_units?: number;
  approve_above_minor_units?: number;
  allowed_categories: string[];
  /** Providers the pass may pay, by ID ("paysh:birdeye.data"); empty means any. */
  allowed_merchants?: string[];
  expires_in_days: number;
};

// --- the catalogs of paid APIs: Pay.sh, Circle's Agent Marketplace, PayAI, Coinbase's Bazaar ---
// Names, descriptions and use cases are written by the providers and the
// catalogs. They are shown as text and never followed as instructions. Prices
// are listings: Algebra asks the endpoint for its real price before paying.

export type ProviderSummary = {
  /** What policy, Spend Passes and receipts call the provider: "paysh:birdeye.data", "circle:birdeye". */
  id: string;
  fqn: string;
  name: string;
  description: string;
  use_case?: string;
  category: string;
  service_url: string;
  host: string;
  endpoint_count: number;
  metered: boolean;
  free_tier: boolean;
  min_price_minor: number;
  max_price_minor: number;
  currency: string;
  page_url: string;
  /** The provider's own site, and its logo when the catalog publishes one. */
  website?: string;
  logo_url?: string;
  /** Solana clusters some endpoint can be paid on: "solana" (mainnet), "solana-devnet". */
  networks: string[];
  source: string;
  /** How it is paid when that isn't x402 on Solana: "monid-balance". Listed, never routed to. */
  billing?: string;
  /** What the directory says it was paid in the last 30 days: calls, and the most distinct payers any endpoint had. Not every catalog says. */
  calls_30d?: number;
  payers_30d?: number;
};

export type ProviderCategory = { name: string; count: number };

export type CatalogStatus = {
  name: string;
  total: number;
  generated_at?: string;
  fetched_at?: string;
  stale?: boolean;
  /** The catalog couldn't be read; its providers are missing from this answer. */
  error?: string;
};

export type ProviderListing = {
  source?: string;
  sources?: CatalogStatus[];
  generated_at?: string;
  fetched_at: string;
  /** Pay.sh couldn't be reached, so this is an older copy. */
  stale?: boolean;
  total: number;
  count: number;
  categories: ProviderCategory[];
  providers: ProviderSummary[];
};

export type ProviderEndpoint = {
  /** What to ask for in POST /api/v1/execute. */
  capability: string;
  method: string;
  path: string;
  url: string;
  pricing: string;
  price_minor: number;
  free: boolean;
  network?: string;
  pay_to?: string;
  /** Every network the endpoint can be paid on, with its listed price there. */
  payments?: { network: string; price_minor: number; pay_to?: string }[];
  /** The request's JSON Schema, when the catalog publishes one. */
  input_schema?: unknown;
  description: string;
  /** The parameters of a templated path, in order. The call's input must name each. */
  path_params?: string[];
  callable: boolean;
  not_callable_reason?: string;
  /** What the directory says this endpoint was paid in the last 30 days. */
  usage?: { calls_30d: number; payers_30d: number; last_called_at?: string };
};

export type ProviderDetail = ProviderSummary & {
  endpoints: ProviderEndpoint[];
  fetched_at: string;
  stale?: boolean;
};

// --- economic intents: what an agent asked Algebra to get done and pay for ---

export type EconEvidence = {
  rail?: string;
  protocol?: string;
  scheme?: string;
  network?: string;
  asset?: string;
  payment_id?: string;
  /** The settlement transaction: a Solana signature on the real rail. */
  transaction?: string;
  amount_minor?: number;
  payer?: string;
  pay_to?: string;
  request_hash?: string;
  result_hash?: string;
  /** True for the sandbox rail: simulated money. */
  test?: boolean;
};

export type EconReservation = {
  id: string;
  attempt: number;
  state: string;
  hold_minor: number;
  provider_id?: string;
  rail?: string;
  quote_minor?: number;
  evidence: EconEvidence;
  outcome?: string;
  created_at: string;
  finished_at?: string;
};

export type EconIntent = {
  id: string;
  spend_pass_id?: string;
  capability: string;
  currency: string;
  budget_max_minor: number;
  state: string;
  commitment?: string;
  fulfillment?: string;
  committed_minor: number;
  attempts: number;
  duplicate_commit_attempts_blocked: number;
  requires_approval: boolean;
  created_at: string;
  updated_at: string;
  expires_at: string;
  reservations: EconReservation[];
  /** The signed Intent Receipt, once the intent has committed. */
  receipt?: string;
  summary: string;
};

/** A Solana cluster Algebra can pay on, and the wallet it pays from. */
export type RailStatus = {
  network: string;
  configured: boolean;
  rail?: string;
  address?: string;
  usdc_minor?: number;
  max_payment_minor?: number;
  error?: string;
};

/** The answer Algebra kept for a paid request, for asking again. The body is the provider's own data: shown, never followed. */
export type KeptResult = {
  intent_id: string;
  content_type: string;
  http_status: number;
  result_hash: string;
  response: unknown;
  response_is_untrusted_provider_data: boolean;
  stored_at: string;
  expires_at: string;
};

export type EconStats = {
  intents: number;
  committed: number;
  open: number;
  unresolved: number;
  execution_attempts: number;
  duplicate_commit_attempts_blocked: number;
  went_unknown: number;
  reconciled: number;
  authorized_minor: number;
  spent_minor: number;
  duplicate_spend_prevented_minor: number;
};

// --- signed Intent Receipts (internal/domain/receipt/intent.go) ---

export type ReceiptMoney = { minor_units: number; currency: string };

export type IntentReceiptClaims = {
  iss: string;
  jti: string;
  iat: number;
  /** The person's pseudonym, never an ID or an email. */
  sub: string;
  v: number;
  intent: { id: string; hash: string; capability: string; effect_key: string; quantity: number; window: string; budget_max: ReceiptMoney };
  authority: { spend_pass_id?: string; policy_version?: string; method: "policy" | "human" };
  reservation: { id: string; executor: { id: string; name?: string; client?: string }; attempt: number };
  provider: { id: string; quote?: ReceiptMoney; settlement_semantics?: string };
  /** How the provider was chosen, when Algebra's router chose it. */
  routing?: { mode: string; plan_hash?: string; quote_hash?: string; candidate_id?: string; plan_rank: number; fallback: boolean };
  execution: {
    protocol?: string;
    scheme?: string;
    request_hash?: string;
    result_hash?: string;
    status: string;
    quality?: { evaluator: string; schema_valid?: boolean; score?: number };
  };
  settlement?: { rail: string; network?: string; asset?: string; amount: ReceiptMoney; transaction?: string; payment_id?: string; payer?: string; pay_to?: string };
  coordination: { attempts: number; duplicate_commit_attempts_blocked: number; reconciliation_required: boolean };
  final_state: { lifecycle: string; commitment: string; fulfillment: string };
  test?: boolean;
};

export type ReceiptVerification = {
  valid: boolean;
  /** The receipt is the one Algebra logged for its intent. */
  recorded: boolean;
  claims?: IntentReceiptClaims;
  reason?: string;
};

// --- accounts (internal/api/v1/auth.go, me.go) ---

export type User = {
  id: string;
  email: string;
  name: string;
  avatar_url?: string;
  email_verified: boolean;
  onboarded: boolean;
  has_password: boolean;
  linked_providers: string[];
  created_at: string;
  /** "demo": shops real listings with a simulated checkout (fake money). */
  mode: "live" | "demo";
};

export type AuthProviders = { password: boolean; google: boolean; github: boolean; demo?: boolean };

export type SessionInfo = {
  id: string;
  user_agent: string;
  ip: string;
  created_at: string;
  last_seen_at: string;
  current: boolean;
};

/**
 * What onboarding sends. The server still stores the older per-person rules
 * (the guardrails) next to the account; Spend Passes carry the real limits.
 */
export type OnboardingAnswers = {
  name?: string;
  use_cases: string[];
  priority: string;
  household: string;
  dietary: string[];
  preferred_merchants: string[];
  guardrails: {
    currency: string;
    approval_threshold_minor_units: number;
    max_per_purchase_minor_units: number;
    max_per_day_minor_units: number;
    blocked_categories: string[];
    international_requires_approval: boolean;
  };
};
