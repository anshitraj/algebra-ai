// Package v1 is Algebra's REST transport. Like internal/mcpserver, every
// handler here is thin: parse the request, call exactly one
// internal/app service method, encode the result. It shares the same
// wiring.Bundle as the MCP server — see internal/platform/wiring — so
// there is exactly one implementation of every commerce rule regardless of
// which transport an integration uses (mandate §53).
package v1

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/billing"
	"github.com/project-algebra/algebra/internal/domain/integrator"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/tenant"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

type API struct {
	b              *wiring.Bundle
	limiter        app.RateLimiter // optional — see app.RateLimiter's doc comment
	allowedOrigins map[string]bool
}

// NewRouter builds the versioned REST API on Go's standard-library
// ServeMux (method+path patterns, no external router dependency needed).
// limiter may be nil (no Redis configured) — see rateLimitMiddleware.
// allowedOrigins is the browser-frontend CORS allow-list — see corsMiddleware.
func NewRouter(b *wiring.Bundle, limiter app.RateLimiter, allowedOrigins []string) http.Handler {
	origins := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		origins[o] = true
	}
	api := &API{b: b, limiter: limiter, allowedOrigins: origins}
	if b.AuthConfig.TrustedProxyHops >= 0 {
		trustedProxyHops = b.AuthConfig.TrustedProxyHops
	}
	mux := http.NewServeMux()

	// --- human accounts (first-party web app) ---
	mux.HandleFunc("GET /api/v1/auth/providers", api.authProviders)
	mux.HandleFunc("GET /api/v1/auth/session", api.getSession)
	mux.HandleFunc("POST /api/v1/auth/signup", api.signUp)
	mux.HandleFunc("POST /api/v1/auth/login", api.signIn)
	mux.HandleFunc("POST /api/v1/auth/logout", api.signOut)
	mux.HandleFunc("POST /api/v1/auth/demo", api.startDemo)
	mux.HandleFunc("POST /api/v1/auth/password/forgot", api.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/password/reset", api.resetPassword)
	mux.HandleFunc("POST /api/v1/auth/agent-token", api.agentToken)
	mux.HandleFunc("GET /api/v1/auth/oauth/{provider}", api.oauthStart)
	mux.HandleFunc("GET /api/v1/auth/oauth/{provider}/callback", api.oauthCallback)

	mux.HandleFunc("GET /api/v1/me", api.getMe)
	mux.HandleFunc("DELETE /api/v1/me", api.deleteMe)
	mux.HandleFunc("GET /api/v1/me/export", api.exportMe)
	mux.HandleFunc("POST /api/v1/me/agent-turns", api.consumeAgentTurn)
	mux.HandleFunc("GET /api/v1/me/plugins", api.listMyPlugins)
	mux.HandleFunc("PUT /api/v1/me/plugins/{id}", api.setMyPlugin)
	mux.HandleFunc("PATCH /api/v1/me", api.updateMe)
	mux.HandleFunc("GET /api/v1/me/sessions", api.listMySessions)
	mux.HandleFunc("POST /api/v1/me/sessions/{id}/revoke", api.revokeMySession)
	mux.HandleFunc("GET /api/v1/me/guardrails", api.getMyGuardrails)
	mux.HandleFunc("PUT /api/v1/me/guardrails", api.setMyGuardrails)
	mux.HandleFunc("POST /api/v1/me/onboarding", api.completeOnboarding)
	mux.HandleFunc("GET /api/v1/me/overview", api.getMyOverview)
	mux.HandleFunc("GET /api/v1/me/intents", api.listMyIntents)
	mux.HandleFunc("GET /api/v1/me/approvals", api.listMyApprovals)
	mux.HandleFunc("GET /api/v1/me/orders", api.listMyOrders)
	mux.HandleFunc("GET /api/v1/me/passes", api.listMyPasses)
	mux.HandleFunc("POST /api/v1/me/passes", api.createMyPass)
	mux.HandleFunc("POST /api/v1/me/passes/{id}/revoke", api.revokeMyPass)
	mux.HandleFunc("POST /api/v1/me/passes/{id}/freeze", api.freezeMyPass)
	mux.HandleFunc("PUT /api/v1/me/passes/{id}/controls", api.setMyPassControls)
	mux.HandleFunc("POST /api/v1/policy/simulate", api.simulatePolicy)
	mux.HandleFunc("GET /api/v1/me/killswitch", api.getKillSwitch)
	mux.HandleFunc("POST /api/v1/me/killswitch", api.setKillSwitch)
	mux.HandleFunc("GET /api/v1/pass", api.getMyAgentPass)
	mux.HandleFunc("GET /.well-known/jwks.json", api.jwks)
	mux.HandleFunc("POST /api/v1/receipts/verify", api.verifyReceipt)
	mux.HandleFunc("GET /api/v1/me/orders/{id}", api.getMyOrder)

	// Economic coordination (docs/ECONOMIC_COORDINATION.md).
	mux.HandleFunc("POST /api/v1/economic-intents", api.createEconomicIntent)
	mux.HandleFunc("GET /api/v1/economic-intents/{id}", api.getEconomicIntent)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/reservations", api.reserveEconomicIntent)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/reservations/{rid}/begin", api.beginEconomicAttempt)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/reservations/{rid}/authorize-payment", api.authorizeEconomicPayment)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/reservations/{rid}/complete", api.completeEconomicAttempt)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/reservations/{rid}/release", api.releaseEconomicAttempt)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/reconcile", api.reconcileEconomicIntent)
	mux.HandleFunc("GET /api/v1/economic-intents/{id}/receipt", api.getEconomicReceipt)
	// Execution: Algebra prices, pays, calls, verifies and signs.
	mux.HandleFunc("POST /api/v1/execute", api.executeOutcome)
	mux.HandleFunc("POST /api/v1/economic-intents/{id}/execute", api.executeEconomicIntent)
	mux.HandleFunc("GET /api/v1/economic-intents/{id}/executions", api.listEconomicExecutions)
	mux.HandleFunc("GET /api/v1/economic-intents/{id}/result", api.getEconomicResult)
	mux.HandleFunc("POST /api/v1/discover/web", api.discoverWeb)
	mux.HandleFunc("GET /api/v1/me/economic-intents/{id}/result", api.getMyEconomicResult)
	mux.HandleFunc("GET /api/v1/me/economic-intents", api.listMyEconomicIntents)
	mux.HandleFunc("POST /api/v1/me/economic-intents", api.createMyEconomicIntent)
	mux.HandleFunc("GET /api/v1/me/economic-intents/stats", api.myEconomicStats)
	mux.HandleFunc("GET /api/v1/me/economic-intents/{id}", api.getMyEconomicIntent)
	mux.HandleFunc("POST /api/v1/me/economic-intents/{id}/approve", api.approveMyEconomicIntent)
	mux.HandleFunc("POST /api/v1/me/economic-intents/{id}/cancel", api.cancelMyEconomicIntent)
	if b.SandboxProvider != nil {
		// SANDBOX x402 provider: simulated data, simulated money.
		mux.HandleFunc("POST /api/v1/sandbox/x402/token-risk", b.SandboxProvider.Serve)
		mux.HandleFunc("GET /api/v1/sandbox/x402/token-risk/operations/{key}", b.SandboxProvider.ServeOperation)
	}
	if b.SandboxPersonas != nil {
		// SANDBOX providers of token.price: alpha, beta, flaky, greedy, trap.
		mux.HandleFunc("POST /api/v1/sandbox/x402/prices/{name}", b.SandboxPersonas.Serve)
	}

	mux.HandleFunc("GET /api/v1/billing", api.getBilling)
	mux.HandleFunc("GET /api/v1/billing/plans", api.getBillingPlans)
	mux.HandleFunc("POST /api/v1/billing/checkout", api.startCheckout)
	mux.HandleFunc("POST /api/v1/billing/confirm", api.confirmCheckout)
	mux.HandleFunc("POST /api/v1/billing/cancel", api.cancelSubscription)
	mux.HandleFunc("POST /api/v1/billing/webhooks/razorpay", api.razorpayWebhook)

	mux.HandleFunc("GET /api/v1/search", api.searchProducts)
	mux.HandleFunc("GET /api/v1/web-search", api.webSearch)
	mux.HandleFunc("GET /api/v1/deals", api.findDeals)
	mux.HandleFunc("GET /api/v1/community-deals", api.communityDeals)

	mux.HandleFunc("POST /api/v1/users", api.createUser)

	mux.HandleFunc("POST /api/v1/agents", api.createAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/revoke", api.revokeAgent)

	mux.HandleFunc("POST /api/v1/integrators", api.createIntegrator)
	mux.HandleFunc("POST /api/v1/integrators/{id}/revoke", api.revokeIntegrator)
	mux.HandleFunc("POST /api/v1/policy/evaluate-transaction", api.evaluateTransaction)

	mux.HandleFunc("POST /api/v1/tenants", api.createTenant)
	mux.HandleFunc("POST /api/v1/tenants/{id}/revoke", api.revokeTenant)
	mux.HandleFunc("POST /api/v1/tenants/{id}/policy-sets", api.setPolicySet)
	mux.HandleFunc("GET /api/v1/tenants/{id}/policy-sets/latest", api.getLatestPolicySet)
	mux.HandleFunc("POST /api/v1/tenants/{id}/webhook-endpoints", api.createWebhookEndpoint)

	mux.HandleFunc("POST /api/v1/payment-intents", api.createPaymentIntent)
	mux.HandleFunc("GET /api/v1/payment-intents/{id}", api.getPaymentIntent)
	mux.HandleFunc("GET /api/v1/payment-intents/{id}/status", api.getPaymentIntent)
	mux.HandleFunc("GET /api/v1/payment-intents/{id}/audit", api.getPaymentIntentAuditTrail)
	mux.HandleFunc("POST /api/v1/payment-intents/{id}/approve", api.approvePaymentIntent)
	mux.HandleFunc("POST /api/v1/payment-intents/{id}/reject", api.rejectPaymentIntent)
	mux.HandleFunc("POST /api/v1/payment-intents/{id}/execute", api.executePaymentIntent)
	mux.HandleFunc("POST /api/v1/payment-intents/{id}/revoke", api.revokePaymentIntent)
	mux.HandleFunc("GET /api/v1/transactions", api.listTransactions)

	mux.HandleFunc("POST /api/v1/intents", api.createIntent)
	mux.HandleFunc("GET /api/v1/intents/{id}", api.getIntent)
	mux.HandleFunc("POST /api/v1/intents/{id}/cancel", api.cancelIntent)
	mux.HandleFunc("POST /api/v1/intents/{id}/discover", api.discover)
	mux.HandleFunc("GET /api/v1/intents/{id}/quotes", api.getQuotes)
	mux.HandleFunc("POST /api/v1/intents/{id}/select-quote", api.selectQuote)
	mux.HandleFunc("POST /api/v1/intents/{id}/request-purchase", api.requestPurchase)
	mux.HandleFunc("GET /api/v1/intents/{id}/policy-preview", api.policyPreview)
	mux.HandleFunc("GET /api/v1/intents/{id}/policy-explain", api.policyExplain)
	mux.HandleFunc("POST /api/v1/intents/{id}/execute", api.execute)
	mux.HandleFunc("GET /api/v1/intents/{id}/order", api.getOrder)
	mux.HandleFunc("GET /api/v1/intents/{id}/receipt", api.getReceipt)
	mux.HandleFunc("POST /api/v1/intents/{id}/complete-authentication", api.completeAuthentication)
	mux.HandleFunc("POST /api/v1/intents/{id}/cancel-order", api.cancelOrder)
	mux.HandleFunc("GET /api/v1/intents/{id}/audit", api.getAuditTrail)

	mux.HandleFunc("GET /api/v1/intents/{id}/approval", api.getApprovalForIntent)
	mux.HandleFunc("POST /api/v1/approvals/{id}/approve", api.approveApproval)
	mux.HandleFunc("POST /api/v1/approvals/{id}/reject", api.rejectApproval)
	mux.HandleFunc("POST /api/v1/approvals/{id}/reapprove", api.reapproveApproval)

	mux.HandleFunc("GET /api/v1/payment-sources", api.listPaymentSources)
	mux.HandleFunc("POST /api/v1/payment-sources", api.addPaymentSource)
	mux.HandleFunc("POST /api/v1/payment-sources/{id}/revoke", api.revokePaymentSource)

	mux.HandleFunc("GET /api/v1/merchants", api.listMerchants)
	mux.HandleFunc("GET /api/v1/providers", api.listProviders)
	mux.HandleFunc("GET /api/v1/providers/{id...}", api.getProvider)
	mux.HandleFunc("GET /api/v1/classes", api.listClasses)
	mux.HandleFunc("GET /api/v1/classes/{id}", api.getClass)
	mux.HandleFunc("GET /api/v1/classes/{id}/health", api.getClassHealth)
	mux.HandleFunc("POST /api/v1/classes/{id}/probe", api.probeClass)
	mux.HandleFunc("GET /api/v1/rails", api.listRails)

	mux.HandleFunc("POST /api/v1/profiles/shipping", api.createShippingProfile)
	mux.HandleFunc("POST /api/v1/profiles/billing", api.createBillingProfile)
	mux.HandleFunc("GET /api/v1/profiles/shipping", api.listShippingAliases)

	mux.HandleFunc("GET /api/v1/commerce-profile", api.getCommerceProfile)
	mux.HandleFunc("PUT /api/v1/commerce-profile/preferences/{category}", api.setCommerceProfilePreferences)
	mux.HandleFunc("PUT /api/v1/commerce-profile/defaults", api.setCommerceProfileDefaults)

	mux.HandleFunc("POST /api/v1/webhooks/{provider}", api.receiveWebhook)

	// The MCP tools, for agents that speak MCP: same services, same limits.
	mux.Handle("/mcp", api.mcpHandler())

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", api.readyz)

	return requestLog(slog.Default(), securityHeaders(corsMiddleware(allowedOrigins, api.rateLimitMiddleware(api.csrfGuard(api.withSession(mux))))))
}

// corsMiddleware is the outermost layer: an OPTIONS preflight is answered
// and returned before it ever reaches rate limiting or a handler, so a
// browser's preflight traffic never consumes a caller's request budget.
// allowedOrigins is matched exactly, never "*" — every origin-gated
// endpoint here accepts Authorization/X-User-ID, and the CORS spec disallows
// a wildcard origin alongside credentialed headers being meaningful anyway.
func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-ID, Idempotency-Key")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// defaultRateLimit/defaultRateLimitWindow bound every caller (agent-token
// or, for unauthenticated endpoints, source IP) to a fixed request budget
// per window (mandate §49). This is a blunt, uniform limit rather than
// per-endpoint tuning — appropriate for a control plane whose expensive
// operations (discovery fan-out, execution) already carry their own
// resource cost, not a substitute for it.
const (
	defaultRateLimit       = 120
	defaultRateLimitWindow = time.Minute
)

// rateLimitMiddleware is a no-op when limiter is nil (Redis not
// configured — acceptable for local development, not for production; see
// docs/LOCAL_DEVELOPMENT.md). When a Redis error occurs — as opposed to a
// clean "over limit" answer — it fails OPEN (allows the request through):
// a cache-layer outage must not cascade into a full API outage over a
// defense-in-depth control that isn't the primary authorization boundary.
func (a *API) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		if isAuthAbuseTarget(r) {
			ok, retry, err := a.limiter.Allow(r.Context(), "ratelimit:auth:"+clientMeta(r).IP, authRateLimit, authRateLimitWindow)
			if err == nil && !ok {
				w.Header().Set("Retry-After", formatSeconds(retry))
				writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "too many attempts — wait a minute and try again"})
				return
			}
		}
		allowed, retryAfter, err := a.limiter.Allow(r.Context(), "ratelimit:"+rateLimitKey(r), defaultRateLimit, defaultRateLimitWindow)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", formatSeconds(retryAfter))
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "rate limit exceeded, retry later"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitKey identifies the caller: the agent token's hash when present
// (never the raw token — this becomes part of a Redis key), then the
// session cookie's hash, otherwise the remote IP for unauthenticated
// endpoints (sign-in, sign-up, GET /merchants). The sandbox providers are
// called by this server itself while it quotes and pays, several times per
// routed call, so they count in a bucket of their own rather than starving
// every anonymous request from the same address.
func rateLimitKey(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/v1/sandbox/x402/") {
		return "sandbox-provider:" + clientIP(r)
	}
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		return "agent:" + agent.HashToken(strings.TrimPrefix(authz, "Bearer "))
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return "session:" + agent.HashToken(c.Value)
	}
	// Never RemoteAddr directly: behind the web app's proxy that is one
	// address for every anonymous visitor, who would all share one bucket.
	return "ip:" + clientIP(r)
}

func formatSeconds(d time.Duration) string {
	secs := int64(d / time.Second)
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error string `json:"error"`
}

// writeError maps a domain sentinel error to the right HTTP status. Never
// includes the raw error's internal detail beyond its message text — none
// of Algebra's error paths embed secrets in error strings, but this is the
// single choke point where that would be caught/fixed if one ever did.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var authErr unauthenticatedError
	switch {
	case errors.As(err, &authErr):
		status = http.StatusUnauthorized
	case errors.Is(err, billing.ErrQuotaExceeded):
		status = http.StatusPaymentRequired
	case errors.Is(err, shared.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, shared.ErrUnauthorized):
		status = http.StatusForbidden
	case errors.Is(err, shared.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, shared.ErrNotImplemented):
		status = http.StatusNotImplemented
	}
	var limited *app.RateLimited
	if errors.As(err, &limited) {
		w.Header().Set("Retry-After", formatSeconds(limited.Retry))
		status = http.StatusTooManyRequests
	}
	writeJSON(w, status, errorBody{Error: err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// resolveAgent extracts and validates the bearer agent token from the
// Authorization header — the same token mechanism internal/mcpserver uses,
// so an agent's identity means the same thing on both transports.
//
// With no Authorization header, a signed-in browser session acts through
// that session's own console agent (same permission set, revoked together
// with the session) — so the web app never has to hold an agent token in
// JavaScript.
func (a *API) resolveAgent(r *http.Request) (*agent.Identity, error) {
	authz := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if authz == "" {
		if sess := sessionFromContext(r.Context()); sess != nil && sess.AgentID != "" {
			ag, err := a.b.Agents.Get(r.Context(), sess.AgentID)
			if err != nil || ag.IsRevoked() {
				return nil, unauthenticatedError{"session agent is no longer valid — sign in again"}
			}
			return ag, nil
		}
		return nil, unauthenticatedError{"sign in, or send Authorization: Bearer <agent_token>"}
	}
	if len(authz) <= len(prefix) || authz[:len(prefix)] != prefix {
		return nil, unauthenticatedError{"malformed Authorization header — expected Bearer <agent_token>"}
	}
	token := authz[len(prefix):]
	ag, err := a.b.Agents.GetByTokenHash(r.Context(), agent.HashToken(token))
	if err != nil {
		return nil, unauthenticatedError{"invalid agent token"}
	}
	if ag.IsRevoked() {
		return nil, unauthenticatedError{"agent token has been revoked"}
	}
	return ag, nil
}

// unauthenticatedError maps to 401 in writeError.
// operatorHeader carries ALGEBRA_OPERATOR_TOKEN on operator-only calls.
const operatorHeader = "X-Algebra-Operator-Token"

// isOperator reports whether the request carries the operator token.
func (a *API) isOperator(r *http.Request) bool {
	want, got := a.b.AuthConfig.OperatorToken, r.Header.Get(operatorHeader)
	return want != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// requireOperator gates B2B registration (new tenants and integrators mint
// root credentials). With no operator token configured it stays open in
// development, for self-serve testing, and is closed in production.
func (a *API) requireOperator(r *http.Request) error {
	if a.isOperator(r) || (a.b.AuthConfig.OperatorToken == "" && !a.b.AuthConfig.Production) {
		return nil
	}
	return unauthenticatedError{msg: "operator token required (" + operatorHeader + ")"}
}

type unauthenticatedError struct{ msg string }

func (e unauthenticatedError) Error() string { return e.msg }

// resolveIntegrator is resolveAgent's counterpart for the standalone
// policy-evaluation surface — a third-party integrator's bearer token,
// checked the same way (hashed lookup + revocation), never an
// agent/shopping-permission token used interchangeably with one.
func (a *API) resolveIntegrator(r *http.Request) (*integrator.Integrator, error) {
	authz := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(authz) <= len(prefix) || authz[:len(prefix)] != prefix {
		return nil, unauthenticatedError{msg: "missing or malformed Authorization: Bearer <integrator_token> header"}
	}
	token := authz[len(prefix):]
	integ, err := a.b.Integrators.GetByTokenHash(r.Context(), agent.HashToken(token))
	if err != nil {
		return nil, unauthenticatedError{msg: "invalid integrator token"}
	}
	if integ.IsRevoked() {
		return nil, unauthenticatedError{msg: "integrator token has been revoked"}
	}
	return integ, nil
}

// resolveTenant is resolveAgent's counterpart for tenant-admin operations
// (setting policy, registering webhook endpoints, listing transactions) — a
// business's own bearer token, checked the same way, never interchangeable
// with an agent or integrator token.
func (a *API) resolveTenant(r *http.Request) (*tenant.Tenant, error) {
	authz := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(authz) <= len(prefix) || authz[:len(prefix)] != prefix {
		return nil, unauthenticatedError{msg: "missing or malformed Authorization: Bearer <tenant_token> header"}
	}
	token := authz[len(prefix):]
	t, err := a.b.Tenants.GetByTokenHash(r.Context(), agent.HashToken(token))
	if err != nil {
		return nil, unauthenticatedError{msg: "invalid tenant token"}
	}
	if t.IsRevoked() {
		return nil, unauthenticatedError{msg: "tenant token has been revoked"}
	}
	return t, nil
}

// currentUserID identifies the human for human-only endpoints (approvals,
// payment sources, addresses, revoking agents). It accepts ONLY a signed-in
// session — never an agent bearer token, which is what keeps "approve this
// spend" out of any agent's reach, including the web app's own LLM loop.
//
// ALGEBRA_DEV_AUTH=true additionally accepts a caller-asserted X-User-ID
// header, for local scripts written before accounts existed. That is not a
// security boundary and must never be enabled on a reachable host.
func (a *API) currentUserID(r *http.Request) (string, error) {
	if userID, ok := a.sessionUserID(r); ok {
		return userID, nil
	}
	if a.b.AuthConfig.DevHeaderAuth {
		if userID := r.Header.Get("X-User-ID"); userID != "" {
			return userID, nil
		}
	}
	return "", unauthenticatedError{"sign in required"}
}
