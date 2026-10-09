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

export type PassControls = {
  max_calls_per_minute: number;
  max_calls_per_provider_per_minute: number;
  new_providers: NewProviderRule;
  new_provider_cap_minor_units: number;
};

export type KillSwitchState = { engaged: boolean; frozen_passes: number; live_passes: number; changed?: number };

export type Verdict = "ALLOW" | "REQUIRE_APPROVAL" | "DENY";

export type CandidateVerdict = {
  provider: string;
  candidate_id?: string;
  network?: string;
  price_minor: number;
  price_source: "live" | "listed";
  verdict: Verdict;
  reasons?: string[];
};

export type RoutedOffer = {
  rank: number;
  provider: string;
  network?: string;
  cost_minor: number;
  expected_latency_ms?: number;
  trust: string;
  score: number;
  components?: Record<string, number>;
  notes?: string[];
  test?: boolean;
};

export type Rejection = { candidate_id?: string; provider?: string; code: string; detail?: string };

export type Simulation = {
  verdict: Verdict;
  reasons: string[] | null;
  would_pay?: CandidateVerdict;
  plan?: { mode: string; plan_hash: string; offers: RoutedOffer[] };
  candidates: CandidateVerdict[];
  rejected?: Rejection[];
  pass: { id: string; remaining_minor: number; calls_last_minute: number; controls: PassControls; frozen: boolean };
  live_quotes: boolean;
  at: string;
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
