// Browser client for Algebra's REST API. Every call goes to this app's own
// origin (/api/v1 is proxied to the Go API — see next.config.ts) and is
// authenticated by the HttpOnly session cookie; no token ever lives in
// JavaScript.

import type {
  AuthProviders,
  EconIntent,
  EconStats,
  IssuedPass,
  KeptResult,
  NewPass,
  OnboardingAnswers,
  PassConnect,
  ProviderDetail,
  ProviderListing,
  RailStatus,
  ReceiptVerification,
  SessionInfo,
  SpendPass,
  User,
} from "./types";
import type {
  ClassDetail,
  ClassRow,
  EndpointHealth,
  KillSwitchState,
  PassControls,
  SimulateBody,
  Simulation,
} from "./routing-types";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type FetchOpts = {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
};

/** Fired on any 401 so the session provider can send the user to sign-in. */
export const UNAUTHORIZED_EVENT = "algebra:unauthorized";

async function apiFetch<T>(path: string, opts: FetchOpts = {}): Promise<T> {
  const { method = "GET", body, headers = {} } = opts;
  const finalHeaders: Record<string, string> = { ...headers };
  if (body !== undefined) finalHeaders["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: finalHeaders,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new ApiError(0, "Can't reach Algebra. Check your connection and try again.");
  }

  const text = await res.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }

  if (!res.ok) {
    if (res.status === 401 && typeof window !== "undefined" && !path.startsWith("/api/v1/auth/")) {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
    }
    const message =
      (data as { error?: string } | undefined)?.error ??
      (res.status >= 500 ? "Algebra's API isn't responding. Try again in a moment." : res.statusText);
    throw new ApiError(res.status, message);
  }
  return data as T;
}

// --- auth ---

export function getAuthProviders() {
  return apiFetch<AuthProviders>("/api/v1/auth/providers");
}

export function getSession() {
  return apiFetch<{ user: User | null }>("/api/v1/auth/session");
}

export function signUp(name: string, email: string, password: string) {
  return apiFetch<{ user: User }>("/api/v1/auth/signup", { method: "POST", body: { name, email, password } });
}

export function signIn(email: string, password: string) {
  return apiFetch<{ user: User }>("/api/v1/auth/login", { method: "POST", body: { email, password } });
}

/** Trades the identity token Privy issued at sign-in for an Algebra session. */
export function signInWithPrivy(identityToken: string) {
  return apiFetch<{ user: User }>("/api/v1/auth/privy", { method: "POST", body: { identity_token: identityToken } });
}

/** One click, no signup: a fresh demo account that pays on a simulated rail. */
export function startDemo() {
  return apiFetch<{ user: User }>("/api/v1/auth/demo", { method: "POST" });
}

export function signOut() {
  return apiFetch<{ ok: boolean }>("/api/v1/auth/logout", { method: "POST" });
}

export function forgotPassword(email: string) {
  return apiFetch<{ ok: boolean }>("/api/v1/auth/password/forgot", { method: "POST", body: { email } });
}

export function resetPassword(token: string, password: string) {
  return apiFetch<{ ok: boolean }>("/api/v1/auth/password/reset", { method: "POST", body: { token, password } });
}

export function oauthStartURL(provider: "google" | "github", next?: string | null) {
  return `/api/v1/auth/oauth/${provider}${next ? `?next=${encodeURIComponent(next)}` : ""}`;
}

// --- me ---

export function updateMe(name: string) {
  return apiFetch<User>("/api/v1/me", { method: "PATCH", body: { name } });
}

/** Erases the signed-in account (DELETE /api/v1/me). `confirm` must be "DELETE". */
export function deleteAccount(confirm: string) {
  return apiFetch<{ ok: boolean }>("/api/v1/me", { method: "DELETE", body: { confirm } });
}

/** Where the browser downloads everything Algebra holds about the user, as JSON. */
export const DATA_EXPORT_URL = "/api/v1/me/export";

export function listSessions() {
  return apiFetch<SessionInfo[]>("/api/v1/me/sessions");
}

export function revokeSession(id: string) {
  return apiFetch<{ ok: boolean }>(`/api/v1/me/sessions/${id}/revoke`, { method: "POST" });
}

export function completeOnboarding(answers: OnboardingAnswers) {
  return apiFetch<User>("/api/v1/me/onboarding", { method: "POST", body: answers });
}

export function listPasses() {
  return apiFetch<{ passes: SpendPass[]; connect: PassConnect }>("/api/v1/me/passes");
}

export function createPass(p: NewPass) {
  return apiFetch<IssuedPass>("/api/v1/me/passes", { method: "POST", body: p });
}

export function revokePass(id: string) {
  return apiFetch<{ ok: boolean }>(`/api/v1/me/passes/${encodeURIComponent(id)}/revoke`, { method: "POST" });
}

// --- the catalogs of paid APIs (public) ---

export function listProviders(params: { q?: string; category?: string; source?: string; network?: string; limit?: number; offset?: number } = {}) {
  const qs = new URLSearchParams();
  if (params.q) qs.set("q", params.q);
  if (params.category) qs.set("category", params.category);
  if (params.source) qs.set("source", params.source);
  if (params.network) qs.set("network", params.network);
  if (params.limit) qs.set("limit", String(params.limit));
  if (params.offset) qs.set("offset", String(params.offset));
  const query = qs.toString();
  return apiFetch<ProviderListing>(`/api/v1/providers${query ? `?${query}` : ""}`);
}

/** id is a provider ID ("paysh:birdeye.data", "circle:birdeye") or a catalog's own name for it. */
export function getProvider(id: string) {
  return apiFetch<ProviderDetail>(`/api/v1/providers/${id.split("/").map(encodeURIComponent).join("/")}`);
}

/** Mainnet and devnet: whether Algebra can pay on each, from which wallet, with what balance. */
export function listRails() {
  return apiFetch<{ rails: RailStatus[]; sandbox: boolean }>("/api/v1/rails");
}

// --- economic intents ---

export function listMyEconomicIntents(limit = 50) {
  return apiFetch<{ intents: EconIntent[] | null }>(`/api/v1/me/economic-intents?limit=${limit}`);
}

export function getMyEconomicIntent(id: string) {
  return apiFetch<EconIntent>(`/api/v1/me/economic-intents/${encodeURIComponent(id)}`);
}

/** The answer kept for a paid request, while it is kept (404 once it isn't, or when the request asked not to keep it). */
export function getMyEconomicResult(id: string) {
  return apiFetch<KeptResult>(`/api/v1/me/economic-intents/${encodeURIComponent(id)}/result`);
}

export function getMyEconomicStats(days = 30) {
  return apiFetch<{ days: number; stats: EconStats; note: string }>(`/api/v1/me/economic-intents/stats?days=${days}`);
}

export function approveEconomicIntent(id: string) {
  return apiFetch<EconIntent>(`/api/v1/me/economic-intents/${encodeURIComponent(id)}/approve`, { method: "POST" });
}

export function cancelEconomicIntent(id: string) {
  return apiFetch<EconIntent>(`/api/v1/me/economic-intents/${encodeURIComponent(id)}/cancel`, { method: "POST" });
}

/** Public: anyone can check a receipt — it carries nothing personal. */
export function verifyReceipt(receipt: string) {
  return apiFetch<ReceiptVerification>("/api/v1/receipts/verify", { method: "POST", body: { receipt } });
}

// --- classes of work, provider health (public reads) ---

export function listClasses() {
  return apiFetch<{ classes: ClassRow[]; built_at: string }>("/api/v1/classes");
}

export function getClass(id: string) {
  return apiFetch<{ class: ClassDetail; health?: EndpointHealth[] }>(`/api/v1/classes/${encodeURIComponent(id)}`);
}

/** Probe every provider of a class now: unpaid requests only. */
export function probeClass(id: string) {
  return apiFetch<{ class: string; probed: number; up: number; health: EndpointHealth[] }>(`/api/v1/classes/${encodeURIComponent(id)}/probe`, {
    method: "POST",
  });
}

// --- the spend firewall ---

export function getKillSwitch() {
  return apiFetch<KillSwitchState>("/api/v1/me/killswitch");
}

export function setKillSwitch(engaged: boolean) {
  return apiFetch<KillSwitchState>("/api/v1/me/killswitch", { method: "POST", body: { engaged } });
}

export function freezePass(id: string, frozen: boolean) {
  return apiFetch<SpendPass>(`/api/v1/me/passes/${encodeURIComponent(id)}/freeze`, { method: "POST", body: { frozen } });
}

export function setPassControls(id: string, c: PassControls) {
  return apiFetch<SpendPass>(`/api/v1/me/passes/${encodeURIComponent(id)}/controls`, { method: "PUT", body: c });
}

/** "Would this be allowed, and who would do it?" Nothing is reserved or paid. */
export function simulatePolicy(b: SimulateBody) {
  return apiFetch<Simulation>("/api/v1/policy/simulate", { method: "POST", body: b });
}
