// Types for classes of work, provider health, the spend firewall and the
// policy dry run (backend/internal/domain/routing/classes.go, backend/internal/app/health_service.go,
// backend/internal/domain/spendpass/controls.go, backend/internal/app/policy_simulate.go).

export type ClassField = { name: string; required: boolean; description: string; example?: string };

/** One row of GET /api/v1/classes. */
export type ClassRow = {
  id: string;
  title: string;
  description: string;
  kind: string;
  fields: ClassField[];
  sample: Record<string, unknown>;
  providers: number;
  routable: number;
  median_price_minor: number;
};

export type ClassMember = {
  provider: string;
  provider_name: string;
  logo_url?: string;
  source: string;
  method: string;
  url: string;
  description?: string;
  price_minor: number;
  networks: string[] | null;
  routable: boolean;
  not_routable_reason?: string;
};

export type ClassDetail = ClassRow & { members: ClassMember[] };

export type HealthStatus = "up" | "input_rejected" | "unpayable" | "down";

export type EndpointHealth = {
  candidate_id: string;
  provider: string;
  capability: string;
  endpoint: string;
  network?: string;
  status: HealthStatus;
  http_status?: number;
  latency_ms: number;
  listed_price_minor?: number;
  live_price_minor?: number;
  overcharges?: boolean;
  checks: number;
  ups: number;
  consecutive_failures: number;
  error?: string;
  checked_at: string;
};

export type NewProviderRule = "allow" | "cap" | "approve";

/** How a pass treats a provider whose identity no person has verified: pay, wait for a yes, or never pay. */
export type UnverifiedRule = "allow" | "approve" | "deny";

/** How a pass treats an endpoint its agent found on the open web: wait for a yes (the default), pay, or never pay. */
export type WebFoundRule = "allow" | "approve" | "deny";

export type PassControls = {
  max_calls_per_minute: number;
  max_calls_per_provider_per_minute: number;
  new_providers: NewProviderRule;
  new_provider_cap_minor_units: number;
  unverified_providers?: UnverifiedRule;
  web_found_providers?: WebFoundRule;
};

export type KillSwitchState = { engaged: boolean; frozen_passes: number; live_passes: number; changed?: number };

export type Verdict = "ALLOW" | "REQUIRE_APPROVAL" | "DENY";

/** What the console calls a verdict: ALLOW runs, HOLD waits for a person's yes, BLOCK pays nothing. */
export type Decision = "ALLOW" | "HOLD" | "BLOCK";

/** What Algebra knows about who it would pay, as separate facts. A payment that worked is not a verified identity. */
export type Seller = {
  known: boolean;
  /** "verified" is identity verification: every check of the standard passed and a person reviewed who runs it. */
  trust?: "verified" | "unverified" | "blocked";
  payment_tested: boolean;
  payee_pinned: boolean;
};

export type CandidateVerdict = {
  provider: string;
  candidate_id?: string;
  network?: string;
  price_minor: number;
  /** The estimated total cost: the provider's price plus any fee. */
  total_minor: number;
  /** "unlisted": the catalog states no price; it is unknown until a provider is asked. */
  price_source: "live" | "listed" | "unlisted";
  verdict: Verdict;
  decision: Decision;
  reasons?: string[];
  seller: Seller;
};

export type RoutedOffer = {
  rank: number;
  provider: string;
  network?: string;
  cost_minor: number;
  expected_latency_ms?: number;
  /** How the speed is known: "observed" (Algebra's own paid calls), "estimate", "price_check", or absent. */
  latency_source?: string;
  calls?: number;
  trust: string;
  score: number;
  components?: Record<string, number>;
  notes?: string[];
  test?: boolean;
};

export type Rejection = { candidate_id?: string; provider?: string; code: string; detail?: string };

export type Simulation = {
  verdict: Verdict;
  decision: Decision;
  reasons: string[] | null;
  /** How the plan was ranked: CHEAPEST, FASTEST or AUTO (shown as Balanced). */
  strategy?: string;
  would_pay?: CandidateVerdict;
  plan?: { mode: string; plan_hash: string; offers: RoutedOffer[] };
  candidates: CandidateVerdict[];
  rejected?: Rejection[];
  /** The best offer each way: cheapest, balanced and fastest, from the same priced offers. */
  recommendations?: Recommendation[];
  /** Every offer that could be paid or approved, ranked for the request's strategy. */
  compared?: Recommendation[];
  pass: { id: string; remaining_minor: number; calls_last_minute: number; controls: PassControls; frozen: boolean };
  live_quotes: boolean;
  at: string;
  /** An answer to the same request was already paid for and is still fresh: paying hands it back for nothing. */
  reuse?: ReusedPurchase;
};

/** An earlier purchase whose answer is handed back instead of paying again (purchase memory). */
export type ReusedPurchase = {
  intent_id: string;
  provider: string;
  paid_minor: number;
  paid_at: string;
  age_seconds: number;
  fresh_for_seconds: number;
};

export type SimulateBody = {
  capability: string;
  input: unknown;
  budget_max_minor: number;
  spend_pass_id: string;
  constraints?: { allowed_networks?: string[] };
  provider_policy?: { strategy?: string };
  live_quotes?: boolean;
};

/** One priced offer as a person compares it (backend/internal/app/policy_simulate.go). */
export type Recommendation = {
  strategy?: "cheapest" | "balanced" | "fastest";
  rank?: number;
  provider: string;
  candidate_id: string;
  network?: string;
  total_minor: number;
  price_source: "live" | "listed" | "unlisted";
  latency_ms?: number;
  latency_source?: string;
  reliability: number;
  /** What one accepted answer is expected to cost: the price over how often answers are accepted, calls paid for nothing counted. */
  cost_per_accepted_minor?: number;
  calls?: number;
  score: number;
  decision: Decision;
  reasons?: string[];
  seller: Seller;
  notes?: string[];
  test?: boolean;
};
