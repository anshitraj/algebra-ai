// Server-side counterpart to ../api-client.ts, for use inside Next.js route
// handlers only. The agent's tool calls authenticate with the session's
// console-agent token (fetched per request from the user's session cookie —
// see getAgentToken), never with the human session itself: an agent token
// can shop within policy but is rejected by every approval endpoint.
// Wraps only the endpoints the agent's tools need — see tools.ts.

import type {
  AuditEvent,
  CommerceProfile,
  DealResults,
  ExecuteResult,
  Guardrails,
  Intent,
  Plugin,
  IntentConstraints,
  IntentItem,
  Merchant,
  Order,
  PolicyDecision,
  Quote,
} from "../types";

const API_URL = process.env.ALGEBRA_API_URL ?? process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export type ServerIdentity = {
  agentToken: string;
  /** "demo" accounts check out through the simulated demo store. */
  mode: "live" | "demo";
  /** The user's per-purchase cap — the budget when they didn't state one. */
  defaultBudgetMinor?: number;
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
  const { method = "GET", body, headers = {} } = opts;

  const finalHeaders: Record<string, string> = { ...headers };
  if (body !== undefined) finalHeaders["Content-Type"] = "application/json";
  if (identity) finalHeaders["Authorization"] = `Bearer ${identity.agentToken}`;

  const res = await fetch(`${API_URL}${path}`, {
    method,
    headers: finalHeaders,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  return parse<T>(res);
}

async function parse<T>(res: Response): Promise<T> {
  const text = await res.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
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
export async function getAgentToken(cookie: string): Promise<ServerIdentity> {
  const { token, mode } = await sessionFetch<{ token: string; mode?: string }>("/api/v1/auth/agent-token", cookie, "POST");
  return { agentToken: token, mode: mode === "demo" ? "demo" : "live" };
}

export type TurnQuota = { ok: true } | { ok: false; limit: number; demo: boolean; retryAfterSeconds: number };

/**
 * Spends one of the user's daily agent messages (POST /api/v1/me/agent-turns).
 * Every message costs an LLM call and billed web searches, so the API caps
 * them per person per day — lower for no-signup demo accounts.
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

export function getGuardrails(cookie: string) {
  return sessionFetch<Guardrails>("/api/v1/me/guardrails", cookie);
}

// --- agent-scoped ---

export function searchProducts(identity: ServerIdentity, query: string, limit = 5) {
  return apiFetch<{ results: unknown[] }>(`/api/v1/search?q=${encodeURIComponent(query)}&limit=${limit}`, identity);
}

export function webSearch(identity: ServerIdentity, query: string, limit = 8, maxPriceMinor?: number) {
  const budget = maxPriceMinor ? `&max_price=${maxPriceMinor}` : "";
  return apiFetch<{ results: unknown[] }>(`/api/v1/web-search?q=${encodeURIComponent(query)}&limit=${limit}${budget}`, identity);
}

export function communityDeals(identity: ServerIdentity, query: string) {
  return apiFetch<{ tips: unknown[]; searched: string[] }>(`/api/v1/community-deals?q=${encodeURIComponent(query)}`, identity);
}

/** The person's plugins (session cookie) — which sources this turn may use. */
export async function getPlugins(cookie: string): Promise<Plugin[]> {
  const r = await sessionFetch<{ plugins: Plugin[] }>("/api/v1/me/plugins", cookie);
  return r.plugins ?? [];
}

export type DealParams = { query: string; merchants?: string[]; priceMinor?: number; banks?: string[]; limit?: number };

export function findDeals(identity: ServerIdentity, p: DealParams) {
  const qs = new URLSearchParams({ q: p.query, limit: String(p.limit ?? 5) });
  if (p.merchants?.length) qs.set("merchant", p.merchants.join(","));
  if (p.priceMinor && p.priceMinor > 0) qs.set("price", String(Math.round(p.priceMinor)));
  if (p.banks?.length) qs.set("banks", p.banks.join(","));
  return apiFetch<DealResults>(`/api/v1/deals?${qs.toString()}`, identity);
}

export function createIntent(
  identity: ServerIdentity,
  items: IntentItem[],
  constraints: IntentConstraints,
  idempotencyKey: string
) {
  return apiFetch<Intent>("/api/v1/intents", identity, {
    method: "POST",
    body: { items, constraints },
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function discover(identity: ServerIdentity, id: string) {
  return apiFetch<{ quotes: Quote[] }>(`/api/v1/intents/${id}/discover`, identity, { method: "POST" });
}

export function getQuotes(identity: ServerIdentity, id: string) {
  return apiFetch<{ quotes: Quote[] }>(`/api/v1/intents/${id}/quotes`, identity);
}

export function selectQuote(identity: ServerIdentity, id: string, quoteId: string) {
  return apiFetch<{ ok: boolean }>(`/api/v1/intents/${id}/select-quote`, identity, {
    method: "POST",
    body: { quote_id: quoteId },
  });
}

export function requestPurchase(identity: ServerIdentity, id: string) {
  return apiFetch<PolicyDecision>(`/api/v1/intents/${id}/request-purchase`, identity, { method: "POST" });
}

export function execute(identity: ServerIdentity, id: string, idempotencyKey: string) {
  return apiFetch<ExecuteResult>(`/api/v1/intents/${id}/execute`, identity, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function getOrder(identity: ServerIdentity, id: string) {
  return apiFetch<Order>(`/api/v1/intents/${id}/order`, identity);
}

export function getAuditTrail(identity: ServerIdentity, id: string) {
  return apiFetch<AuditEvent[]>(`/api/v1/intents/${id}/audit`, identity);
}

export function cancelIntent(identity: ServerIdentity, id: string) {
  return apiFetch<Intent>(`/api/v1/intents/${id}/cancel`, identity, { method: "POST" });
}

export function listMerchants() {
  return apiFetch<Merchant[]>("/api/v1/merchants", null);
}

export function getCommerceProfile(identity: ServerIdentity) {
  return apiFetch<CommerceProfile>("/api/v1/commerce-profile", identity);
}

export function setCommercePreferences(identity: ServerIdentity, category: string, attributes: Record<string, unknown>) {
  return apiFetch<CommerceProfile>(`/api/v1/commerce-profile/preferences/${encodeURIComponent(category)}`, identity, {
    method: "PUT",
    body: { attributes },
  });
}
