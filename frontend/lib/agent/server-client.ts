// Server-side counterpart to ../api-client.ts, for use inside Next.js route
// handlers only. The agent's tool calls authenticate with the session's
// console-agent token (fetched per request from the user's session cookie —
// see getAgentToken), never with the human session itself: an agent token
// can pay within a Spend Pass but is rejected by every approval endpoint.
// Wraps only the endpoints the agent's tools need — see tools.ts.

import type { EconIntent, JobPass, ProviderDetail, ProviderListing, RailStatus, SpendPass } from "../types";

const API_URL = process.env.ALGEBRA_API_URL ?? process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

/** A cluster, or Algebra's sandbox, which is where a demo account pays. */
export type ChatNetwork = "solana" | "solana-devnet" | "sandbox";

export type ServerIdentity = {
  agentToken: string;
  /**
   * The browser's session cookie, kept server-side and never sent to the model:
   * when the person clicks a card to pay, that click is their approval, recorded
   * on the intent with their own session (an agent token can never approve); and
   * after a payment from an on-chain pass, it reads that pass's pull for the card.
   */
  cookie?: string;
  /** "demo" accounts: a person trying Algebra without signing up. */
  mode: "live" | "demo";
  /** The Spend Pass this chat pays under, picked in the console. Without one the agent can look, not pay. */
  passId?: string;
  /** The network every payment in this chat is restricted to. */
  network: ChatNetwork;
  /** The pass itself, as read this turn: its rules are reported with each payment. */
  pass?: SpendPass;
  /** The person wants to say yes to every payment, whatever the pass allows. */
  askFirst?: boolean;
  /** How the person wants providers picked for this chat; it wins over what the model asks for. */
  pick?: "cheapest" | "balanced" | "fastest";
  /** The Job Pass this turn's chosen payments run under: the task's budget, set aside from the pass. */
  jobId?: string;
};

export class ServerApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ServerApiError";
    this.status = status;
  }
}

type FetchOpts = {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
};

async function apiFetch<T>(path: string, identity: ServerIdentity | null, opts: FetchOpts = {}): Promise<T> {
  const res = await rawFetch(path, identity, opts);
  return parse<T>(res);
}

function rawFetch(path: string, identity: ServerIdentity | null, opts: FetchOpts = {}) {
  const { method = "GET", body, headers = {} } = opts;
  const finalHeaders: Record<string, string> = { ...headers };
  if (body !== undefined) finalHeaders["Content-Type"] = "application/json";
  if (identity) finalHeaders["Authorization"] = `Bearer ${identity.agentToken}`;
  return fetch(`${API_URL}${path}`, {
    method,
    headers: finalHeaders,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
}

async function readJSON(res: Response): Promise<unknown> {
  const text = await res.text();
  try {
    return text ? JSON.parse(text) : undefined;
  } catch {
    return undefined;
  }
}

async function parse<T>(res: Response): Promise<T> {
  const data = await readJSON(res);
  if (!res.ok) {
    const message = (data as { error?: string } | undefined)?.error ?? res.statusText;
    throw new ServerApiError(res.status, message);
  }
  return data as T;
}

// --- session-scoped (the browser's cookie, forwarded by the route handler) ---

async function sessionFetch<T>(path: string, cookie: string, method = "GET"): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, { method, headers: { Cookie: cookie }, cache: "no-store" });
  return parse<T>(res);
}

/** A task's budget could not be set aside: what the Spend Pass has left, and what was asked for. */
export type JobShort = { short: true; leftMinor: number; wantedMinor: number };

/**
 * Opens a Job Pass for one chat turn's chosen payments, with the person's own
 * session: their whole most-it-could-cost is set aside from the pass at once,
 * or nothing is. Null when this API has no Job Passes (an older server).
 */
export async function openTaskJob(cookie: string, p: { passId: string; label: string; budgetMinor: number; seconds: number }): Promise<JobPass | JobShort | null> {
  const res = await fetch(`${API_URL}/api/v1/me/jobs`, {
    method: "POST",
    headers: { Cookie: cookie, "Content-Type": "application/json" },
    body: JSON.stringify({ spend_pass_id: p.passId, label: p.label, budget_minor: p.budgetMinor, expires_in_seconds: p.seconds }),
    cache: "no-store",
  });
  if (res.status === 404 || res.status === 501) return null;
  const body = (await readJSON(res)) as (JobPass & { reason_codes?: string[]; left_minor?: number; wanted_minor?: number; error?: string }) | undefined;
  if (res.status === 403 && body?.reason_codes?.includes("JOB_PARENT_SHORT")) {
    return { short: true, leftMinor: body.left_minor ?? 0, wantedMinor: body.wanted_minor ?? p.budgetMinor };
  }
  if (!res.ok || !body) throw new ServerApiError(res.status, body?.error ?? res.statusText);
  return body;
}

/** Closes a turn's Job Pass: what its payments didn't use goes back to the Spend Pass. */
export async function closeTaskJob(cookie: string, id: string): Promise<JobPass> {
  const res = await fetch(`${API_URL}/api/v1/me/jobs/${encodeURIComponent(id)}/close`, { method: "POST", headers: { Cookie: cookie }, cache: "no-store" });
  return parse<JobPass>(res);
}

/** The session's console-agent token. Throws a 401 ServerApiError if the cookie is missing or stale. */
export async function getAgentToken(cookie: string): Promise<{ agentToken: string; mode: "live" | "demo" }> {
  const { token, mode } = await sessionFetch<{ token: string; mode?: string }>("/api/v1/auth/agent-token", cookie, "POST");
  return { agentToken: token, mode: mode === "demo" ? "demo" : "live" };
}

export type TurnQuota = { ok: true } | { ok: false; limit: number; demo: boolean; retryAfterSeconds: number };

/**
 * Spends one of the user's daily agent messages (POST /api/v1/me/agent-turns).
 * Every message costs an LLM call, so the API caps them per person per day —
 * lower for no-signup demo accounts.
 */
export async function consumeAgentTurn(cookie: string): Promise<TurnQuota> {
  const res = await fetch(`${API_URL}/api/v1/me/agent-turns`, { method: "POST", headers: { Cookie: cookie }, cache: "no-store" });
  if (res.status === 429) {
    const d = (await res.json().catch(() => ({}))) as { limit?: number; demo?: boolean; retry_after_seconds?: number };
    return { ok: false, limit: d.limit ?? 0, demo: !!d.demo, retryAfterSeconds: d.retry_after_seconds ?? 0 };
  }
  await parse(res);
  return { ok: true };
}

/** The pulls an on-chain Spend Pass has made from its vault (session cookie). Empty for a pass that isn't on chain. */
export async function getOnchainPulls(cookie: string, passId: string): Promise<{ intent_id: string; pull_signature: string; network: string }[]> {
  const r = await sessionFetch<{ pulls: { intent_id: string; pull_signature: string; network: string }[] | null }>(
    `/api/v1/me/passes/${encodeURIComponent(passId)}/onchain`,
    cookie,
  );
  return r.pulls ?? [];
}

/** The person's Spend Passes (session cookie). */
export async function getPasses(cookie: string): Promise<SpendPass[]> {
  const r = await sessionFetch<{ passes: SpendPass[] | null }>("/api/v1/me/passes", cookie);
  return r.passes ?? [];
}

/** Mainnet and devnet: whether this server can pay there, and from which wallet (signed in only). */
export async function getRails(cookie: string): Promise<RailStatus[]> {
  const r = await sessionFetch<{ rails: RailStatus[] | null }>("/api/v1/rails", cookie);
  return r.rails ?? [];
}

/** The on-chain Spend Pass program per network, and whether Algebra's payer can pay fees (public). */
export async function getOnchainNetworks(): Promise<{ network: string; required: boolean; signer_ready?: boolean }[]> {
  const r = await apiFetch<{ networks: { network: string; required: boolean; signer_ready?: boolean }[] | null }>("/api/v1/onchain/networks", null);
  return r.networks ?? [];
}

// --- public ---

export function searchProviders(p: { query: string; network: string; category?: string; limit?: number }) {
  const qs = new URLSearchParams({ network: p.network, limit: String(p.limit ?? 8) });
  if (p.query) qs.set("q", p.query);
  if (p.category) qs.set("category", p.category);
  return apiFetch<ProviderListing>(`/api/v1/providers?${qs.toString()}`, null);
}

export function getProvider(id: string) {
  return apiFetch<ProviderDetail>(`/api/v1/providers/${id.split("/").map(encodeURIComponent).join("/")}`, null);
}

// --- agent-scoped ---

/**
 * An execution's answer, whatever its status: a refusal (approval needed,
 * nothing to route to, every provider failed) is an outcome the agent must
 * read and explain, not an exception.
 */
export type ExecutionAnswer = { status: number; body: Record<string, unknown> };

async function answer(res: Response): Promise<ExecutionAnswer> {
  const body = ((await readJSON(res)) ?? {}) as Record<string, unknown>;
  if (res.status === 401 || res.status === 404 || res.status === 400 || res.status === 501) {
    throw new ServerApiError(res.status, typeof body.error === "string" ? body.error : res.statusText);
  }
  return { status: res.status, body };
}

export type ExecuteBody = {
  capability: string;
  /** Empty for a class of work: Algebra routes across every provider of it. */
  providers: string[];
  input: unknown;
  budget_max_minor: number;
  provider_policy?: { strategy?: string };
  /** Names the request: the same window is the same request, paid at most once. */
  window?: string;
  /** Ask for the person's yes before anything is paid, whatever the pass allows. */
  require_approval?: boolean;
  /** Pay for a new answer even when a fresh one was already paid for (purchase memory). */
  fresh?: boolean;
};

/** POST /api/v1/policy/simulate: what /execute would do, without reserving or paying anything. */
export async function simulate(identity: ServerIdentity, b: ExecuteBody): Promise<ExecutionAnswer> {
  const res = await rawFetch("/api/v1/policy/simulate", identity, {
    method: "POST",
    body: {
      ...b,
      currency: "USDC",
      constraints: { allowed_networks: [identity.network] },
      spend_pass_id: identity.passId,
      // The dry run shows what a real request would meet, including the yes the person asked for.
      require_approval: identity.askFirst || undefined,
      live_quotes: true,
    },
  });
  return answer(res);
}

/** POST /api/v1/execute: price, check against the pass, pay on this chat's cluster, call, verify. */
export async function execute(identity: ServerIdentity, b: ExecuteBody): Promise<ExecutionAnswer> {
  const res = await rawFetch("/api/v1/execute", identity, {
    method: "POST",
    body: {
      ...b,
      currency: "USDC",
      constraints: { allowed_networks: [identity.network] },
      spend_pass_id: identity.passId,
      job_id: identity.jobId,
      // Only ever adds a step: the person's yes, before anything is paid.
      require_approval: b.require_approval || identity.askFirst || undefined,
    },
  });
  return answer(res);
}

/**
 * Approves an intent as the person: their own session, never the agent's
 * token. Only the route handler calls it, and only for a card the person
 * clicked in the browser (the model can't reach it).
 */
export async function approveAsPerson(identity: ServerIdentity, intentId: string): Promise<boolean> {
  if (!identity.cookie) return false;
  const res = await fetch(`${API_URL}/api/v1/me/economic-intents/${encodeURIComponent(intentId)}/approve`, {
    method: "POST",
    headers: { Cookie: identity.cookie },
    cache: "no-store",
  });
  return res.ok;
}

/** Runs an intent that already exists, typically one the person has just approved. */
export async function executeIntent(identity: ServerIdentity, intentId: string, providers: string[]): Promise<ExecutionAnswer> {
  const res = await rawFetch(`/api/v1/economic-intents/${encodeURIComponent(intentId)}/execute`, identity, {
    method: "POST",
    body: { providers, spend_pass_id: identity.passId, job_id: identity.jobId },
  });
  return answer(res);
}

export function getIntent(identity: ServerIdentity, intentId: string) {
  return apiFetch<EconIntent>(`/api/v1/economic-intents/${encodeURIComponent(intentId)}`, identity);
}
