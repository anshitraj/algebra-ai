// Server-side counterpart to ../api-client.ts, for use inside Next.js route
// handlers only. The agent's tool calls authenticate with the session's
// console-agent token (fetched per request from the user's session cookie —
// see getAgentToken), never with the human session itself: an agent token
// can pay within a Spend Pass but is rejected by every approval endpoint.
// Wraps only the endpoints the agent's tools need — see tools.ts.

import type { EconIntent, ProviderDetail, ProviderListing, RailStatus, SpendPass } from "../types";

const API_URL = process.env.ALGEBRA_API_URL ?? process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export type ChatNetwork = "solana" | "solana-devnet";

export type ServerIdentity = {
  agentToken: string;
  /** "demo" accounts: a person trying Algebra without signing up. */
  mode: "live" | "demo";
  /** The Spend Pass this chat pays under, picked in the console. Without one the agent can look, not pay. */
  passId?: string;
  /** The Solana cluster every payment in this chat is restricted to. */
  network: ChatNetwork;
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
  providers: string[];
  input: unknown;
  budget_max_minor: number;
};

/** POST /api/v1/execute: price, check against the pass, pay on this chat's cluster, call, verify. */
export async function execute(identity: ServerIdentity, b: ExecuteBody): Promise<ExecutionAnswer> {
  const res = await rawFetch("/api/v1/execute", identity, {
    method: "POST",
    body: {
      ...b,
      currency: "USDC",
      constraints: { allowed_networks: [identity.network] },
      spend_pass_id: identity.passId,
    },
  });
  return answer(res);
}

/** Runs an intent that already exists, typically one the person has just approved. */
export async function executeIntent(identity: ServerIdentity, intentId: string, providers: string[]): Promise<ExecutionAnswer> {
  const res = await rawFetch(`/api/v1/economic-intents/${encodeURIComponent(intentId)}/execute`, identity, {
    method: "POST",
    body: { providers, spend_pass_id: identity.passId },
  });
  return answer(res);
}

export function getIntent(identity: ServerIdentity, intentId: string) {
  return apiFetch<EconIntent>(`/api/v1/economic-intents/${encodeURIComponent(intentId)}`, identity);
}
