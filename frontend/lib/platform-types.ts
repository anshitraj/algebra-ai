// Organizations, projects, API keys (backend/internal/domain/tenancy) and
// Provider Intelligence (backend/internal/domain/intel), as the API sends them.

export type Environment = "sandbox" | "devnet" | "mainnet";
export type Scope = "read" | "simulate" | "execute";
export type Role = "owner" | "admin" | "developer" | "viewer";
export type Strategy = "" | "cheapest" | "fastest" | "balanced";

/** A commercial plan and what it allows; -1 means no limit. Mirrors backend/internal/domain/tenancy/plans.go. */
export type Plan = {
  id: string;
  name: string;
  monthly_usd_cents: number;
  projects: number;
  managed_executions: number;
  members: number;
  trial: boolean;
};

export type Organization = {
  id: string;
  name: string;
  plan: string;
  trial_plan?: string;
  trial_ends_at?: string;
  created_at: string;
  role: Role;
  /** The plan in force now: the trial's while it runs. */
  effective_plan: string;
  entitlements: Plan;
  trial_active: boolean;
  /** False once the organization has had its one trial. */
  trial_available: boolean;
};

/** A project's metered use this calendar month (UTC). Spend is what providers were paid, in micro-USDC. */
export type ProjectUsage = {
  project_id: string;
  since: string;
  requests: number;
  by_state: Record<string, number>;
  by_network: { network: string; environment?: Environment; executions: number; spent_minor: number }[];
  /** Committed requests on devnet and mainnet: what a plan counts. Sandbox runs are not counted. */
  managed_executions: number;
  plan: string;
  included_executions: number;
};

export type Project = {
  id: string;
  org_id: string;
  name: string;
  default_strategy: Strategy;
  created_at: string;
  archived_at?: string;
};

export type APIKey = {
  id: string;
  project_id: string;
  environment: Environment;
  name: string;
  prefix: string;
  scopes: Scope[];
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
  rotated_from?: string;
};

/** A new key with its secret: shown once, never again. */
export type IssuedKey = APIKey & { secret: string };

export type NewKey = { name: string; environment: Environment; scopes: Scope[]; expires_in_days?: number };

export type PlatformEvent = {
  id: string;
  org_id?: string;
  project_id?: string;
  actor: string;
  event: string;
  data?: Record<string, unknown>;
  created_at: string;
};

// --- Provider Intelligence ---

export type Trust = "verified" | "unverified" | "blocked";

/** What is known about an endpoint: a listing is not a quote, a quote is not a test, a test is not verification. */
export type IntelLevels = { listed: boolean; quoted: boolean; tested: boolean; verified: boolean; executable: boolean };

export type IntelEndpoint = {
  id: string;
  source: string;
  provider_id: string;
  provider_name: string;
  capability: string;
  method: string;
  url: string;
  network: string;
  protocol: string;
  asset: string;
  listed_price_minor: number;
  pay_to?: string;
  pinned_pay_to?: string;
  callable: boolean;
  trust: Trust;
  trust_reason?: string;
  status: "active" | "removed" | "unreachable";
  latency_ms?: number;
  quoted_at?: string;
  tested_at?: string;
  verified_at?: string;
  first_seen_at: string;
  last_seen_at: string;
  last_changed_at: string;
  updated_at: string;
  levels: IntelLevels;
};

export type IntelChange = {
  id: string;
  endpoint_id: string;
  provider_id: string;
  source?: string;
  kind: string;
  suspicious: boolean;
  before?: Record<string, unknown>;
  after?: Record<string, unknown>;
  actor: string;
  run_id?: string;
  created_at: string;
};

export type IntelEndpointDetail = IntelEndpoint & { changes: IntelChange[] };

export type SourceRun = {
  name: string;
  ok: boolean;
  stale?: boolean;
  error?: string;
  attempts: number;
  seen: number;
  added: number;
  changed: number;
  removed: number;
  duration_ms: number;
  kept_last_good?: boolean;
};

export type SyncRun = {
  id: string;
  trigger: string;
  status: "running" | "ok" | "partial" | "failed";
  sources: SourceRun[] | null;
  seen: number;
  added: number;
  changed: number;
  removed: number;
  error?: string;
  started_at: string;
  finished_at?: string;
};

export type IntelStatus = {
  schedule: string;
  job?: {
    name: string;
    next_run_at: string;
    lease_owner?: string;
    lease_until?: string;
    last_finished_at?: string;
    last_success_at?: string;
    last_error?: string;
  };
  runs: SyncRun[] | null;
  sources: { name: string; last_success_at?: string; last_error?: string; stale: boolean }[];
  summary: {
    endpoints: number;
    providers: number;
    by_trust: Record<string, number>;
    by_status: Record<string, number>;
    by_network: Record<string, number>;
    by_source: Record<string, number>;
    quoted: number;
    tested: number;
    executable: number;
    new_this_week: number;
  };
  discovery: Record<string, string>;
};

export type IntelQuery = {
  source?: string;
  network?: string;
  protocol?: string;
  trust?: string;
  level?: string;
  capability?: string;
  q?: string;
  sort?: string;
  limit?: number;
  offset?: number;
};
