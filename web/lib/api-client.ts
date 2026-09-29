// Browser client for Algebra's REST API. Every call goes to this app's own
// origin (/api/v1 is proxied to the Go API — see next.config.ts) and is
// authenticated by the HttpOnly session cookie; no token ever lives in
// JavaScript.

import type {
  Approval,
  ApprovalActivity,
  AuditEvent,
  AuthProviders,
  BillingStatus,
  CheckoutSession,
  CommerceProfile,
  ExecuteResult,
  Guardrails,
  GuardrailsResponse,
  Intent,
  IntentActivity,
  IntentConstraints,
  IntentItem,
  IssuedPass,
  NewPass,
  OrderDetail,
  PassConnect,
  ReceiptVerification,
  SpendPass,
  Merchant,
  OnboardingAnswers,
  Order,
  Overview,
  PaymentSource,
  Plugin,
  PolicyDecision,
  Quote,
  SessionInfo,
  ShippingProfile,
  Subscription,
  User,
} from "./types";

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

/** One click, no signup: a fresh demo account (real listings, simulated checkout). */
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

export function listPlugins() {
  return apiFetch<{ plugins: Plugin[] }>("/api/v1/me/plugins").then((r) => r.plugins);
}

/** Switches a plugin on or off; `config` sets the Reddit plugin's subreddits. */
export function setPlugin(id: string, enabled: boolean, config?: { subreddits: string[] }) {
  return apiFetch<Plugin>(`/api/v1/me/plugins/${encodeURIComponent(id)}`, { method: "PUT", body: config ? { enabled, config } : { enabled } });
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

export function getGuardrails() {
  return apiFetch<GuardrailsResponse>("/api/v1/me/guardrails");
}

export function setGuardrails(g: Guardrails) {
  return apiFetch<GuardrailsResponse>("/api/v1/me/guardrails", { method: "PUT", body: g });
}

export function completeOnboarding(answers: OnboardingAnswers) {
  return apiFetch<User>("/api/v1/me/onboarding", { method: "POST", body: answers });
}

export function getOverview() {
  return apiFetch<Overview>("/api/v1/me/overview");
}

export function listMyIntents(limit = 25) {
  return apiFetch<IntentActivity[]>(`/api/v1/me/intents?limit=${limit}`);
}

export function listMyApprovals() {
  return apiFetch<ApprovalActivity[]>("/api/v1/me/approvals");
}

export function listMyOrders(limit = 50) {
  return apiFetch<Order[]>(`/api/v1/me/orders?limit=${limit}`);
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

/** Public: anyone can check a receipt — it carries nothing personal. */
export function verifyReceipt(receipt: string) {
  return apiFetch<ReceiptVerification>("/api/v1/receipts/verify", { method: "POST", body: { receipt } });
}

export function getMyOrder(id: string) {
  return apiFetch<OrderDetail>(`/api/v1/me/orders/${encodeURIComponent(id)}`);
}

// --- billing (Algebra's own plans — never purchase money) ---

export function getBilling() {
  return apiFetch<BillingStatus>("/api/v1/billing");
}

export function startCheckout() {
  return apiFetch<CheckoutSession>("/api/v1/billing/checkout", { method: "POST" });
}

export function confirmCheckout(r: { razorpay_payment_id: string; razorpay_subscription_id: string; razorpay_signature: string }) {
  return apiFetch<Subscription>("/api/v1/billing/confirm", { method: "POST", body: r });
}

export function cancelSubscription() {
  return apiFetch<Subscription>("/api/v1/billing/cancel", { method: "POST" });
}

// --- intents ---

export function createIntent(items: IntentItem[], constraints: IntentConstraints, idempotencyKey: string) {
  return apiFetch<Intent>("/api/v1/intents", {
    method: "POST",
    body: { items, constraints },
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function getIntent(id: string) {
  return apiFetch<Intent>(`/api/v1/intents/${id}`);
}

export function cancelIntent(id: string) {
  return apiFetch<Intent>(`/api/v1/intents/${id}/cancel`, { method: "POST" });
}

export function discover(id: string) {
  return apiFetch<{ quotes: Quote[] }>(`/api/v1/intents/${id}/discover`, { method: "POST" });
}

export function getQuotes(id: string) {
  return apiFetch<{ quotes: Quote[] }>(`/api/v1/intents/${id}/quotes`);
}

export function selectQuote(id: string, quoteId: string) {
  return apiFetch<{ ok: boolean }>(`/api/v1/intents/${id}/select-quote`, {
    method: "POST",
    body: { quote_id: quoteId },
  });
}

export function requestPurchase(id: string) {
  return apiFetch<PolicyDecision>(`/api/v1/intents/${id}/request-purchase`, { method: "POST" });
}

export function policyPreview(id: string) {
  return apiFetch<PolicyDecision>(`/api/v1/intents/${id}/policy-preview`);
}

export function policyExplain(id: string) {
  return apiFetch<PolicyDecision>(`/api/v1/intents/${id}/policy-explain`);
}

export function execute(id: string, idempotencyKey: string) {
  return apiFetch<ExecuteResult>(`/api/v1/intents/${id}/execute`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
  });
}

export function getOrder(id: string) {
  return apiFetch<Order>(`/api/v1/intents/${id}/order`);
}

export function getReceipt(id: string) {
  return apiFetch<Order>(`/api/v1/intents/${id}/receipt`);
}

export function cancelOrder(id: string) {
  return apiFetch<Order>(`/api/v1/intents/${id}/cancel-order`, { method: "POST" });
}

export function getAuditTrail(id: string) {
  return apiFetch<AuditEvent[]>(`/api/v1/intents/${id}/audit`);
}

// --- approvals (human session only — an agent token is rejected) ---

export function getApprovalForIntent(intentId: string) {
  return apiFetch<Approval>(`/api/v1/intents/${intentId}/approval`);
}

export function approveApproval(id: string) {
  return apiFetch<{ approval_id: string; status: string }>(`/api/v1/approvals/${id}/approve`, { method: "POST" });
}

export function rejectApproval(id: string) {
  return apiFetch<{ approval_id: string; status: string }>(`/api/v1/approvals/${id}/reject`, { method: "POST" });
}

export function reapproveApproval(id: string) {
  return apiFetch<{ approval_id: string; status: string }>(`/api/v1/approvals/${id}/reapprove`, { method: "POST" });
}

// --- payment sources ---

export function listPaymentSources() {
  return apiFetch<PaymentSource[] | null>("/api/v1/payment-sources");
}

export function addPaymentSource(providerNonce: string, alias: string, nickname?: string) {
  return apiFetch<PaymentSource>("/api/v1/payment-sources", {
    method: "POST",
    body: { provider_nonce: providerNonce, alias, nickname },
  });
}

export function revokePaymentSource(id: string) {
  return apiFetch<{ ok: boolean }>(`/api/v1/payment-sources/${id}/revoke`, { method: "POST" });
}

// --- merchants ---

/**
 * The stores an account actually uses: the mock test store is never shown,
 * and the demo checkout only to demo accounts — mirroring how the API routes
 * purchases by account mode.
 */
export function merchantsFor(list: Merchant[], mode: User["mode"] | undefined) {
  return list.filter((m) => m.name !== "mock" && (mode === "demo" || m.name !== "demo_checkout"));
}

export function listMerchants() {
  return apiFetch<Merchant[]>("/api/v1/merchants");
}

// --- addresses ---

export type { ShippingProfile };

export function createShippingProfile(alias: string, profile: ShippingProfile) {
  return apiFetch<{ ok: boolean }>("/api/v1/profiles/shipping", { method: "POST", body: { alias, profile } });
}

export function listShippingAliases() {
  return apiFetch<string[] | null>("/api/v1/profiles/shipping");
}

// --- commerce profile ---

export function getCommerceProfile() {
  return apiFetch<CommerceProfile>("/api/v1/commerce-profile");
}

export function setCommerceProfilePreferences(category: string, attributes: Record<string, unknown>) {
  return apiFetch<CommerceProfile>(`/api/v1/commerce-profile/preferences/${encodeURIComponent(category)}`, {
    method: "PUT",
    body: { attributes },
  });
}

export function setCommerceProfileDefaults(shippingAlias?: string, paymentAlias?: string) {
  return apiFetch<CommerceProfile>("/api/v1/commerce-profile/defaults", {
    method: "PUT",
    body: { shipping_alias: shippingAlias, payment_alias: paymentAlias },
  });
}
