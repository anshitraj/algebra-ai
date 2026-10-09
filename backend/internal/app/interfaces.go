// Package app holds Algebra's application services — the ONE place
// commerce business logic lives. REST (internal/api/v1), MCP
// (internal/mcpserver), and the future TypeScript SDK all call these same
// services; none of them re-implement or duplicate this logic (mandate
// §53). Services depend on the storage/provider interfaces declared in this
// file, never on a concrete postgres/redis/connector type directly — those
// are injected at cmd/api / cmd/mcp startup.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/integrator"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
	"github.com/project-algebra/algebra/internal/domain/policyset"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/tenant"
	"github.com/project-algebra/algebra/internal/domain/websearch"
	"github.com/project-algebra/algebra/policy"
)

type IntentStore interface {
	Create(ctx context.Context, pi *intent.PurchaseIntent) error
	Get(ctx context.Context, id string) (*intent.PurchaseIntent, error)
	Update(ctx context.Context, pi *intent.PurchaseIntent) error
}

type AgentStore interface {
	GetByTokenHash(ctx context.Context, hash string) (*agent.Identity, error)
	Get(ctx context.Context, id string) (*agent.Identity, error)
	Create(ctx context.Context, a *agent.Identity) error
	Revoke(ctx context.Context, id string, revokedAt time.Time) error
}

// IntegratorStore backs IntegratorService/TransactionPolicyService — see
// internal/domain/integrator's package doc for what an Integrator is and
// how it differs from an AgentStore-backed AI shopping agent.
type IntegratorStore interface {
	GetByTokenHash(ctx context.Context, hash string) (*integrator.Integrator, error)
	Get(ctx context.Context, id string) (*integrator.Integrator, error)
	Create(ctx context.Context, i *integrator.Integrator) error
	Revoke(ctx context.Context, id string, revokedAt time.Time) error
}

type QuoteStore interface {
	Save(ctx context.Context, intentID string, q *quote.CheckoutQuote) error
	Get(ctx context.Context, quoteID string) (*quote.CheckoutQuote, error)
	ListByIntent(ctx context.Context, intentID string) ([]*quote.CheckoutQuote, error)
}

type PolicyDecisionStore interface {
	Save(ctx context.Context, intentID string, dec *policy.PolicyDecision) error
	GetLatestByIntent(ctx context.Context, intentID string) (*policy.PolicyDecision, error)
}

type ApprovalStore interface {
	Create(ctx context.Context, a *approval.Approval) error
	Get(ctx context.Context, id string) (*approval.Approval, error)
	GetByIntent(ctx context.Context, intentID string) (*approval.Approval, error)
	// GetByPaymentIntent is GetByIntent's counterpart for the
	// AgenticPaymentIntent flow — see approval.Approval's doc comment.
	GetByPaymentIntent(ctx context.Context, paymentIntentID string) (*approval.Approval, error)
	Update(ctx context.Context, a *approval.Approval) error
	// MarkConsumed atomically transitions an approval from APPROVED to
	// CONSUMED (an UPDATE ... WHERE status = 'APPROVED' in the Postgres
	// implementation) and reports whether THIS call won the race. This is
	// what makes concurrent execution attempts on the same approval safe —
	// only one caller ever gets claimed == true (mandate §32).
	MarkConsumed(ctx context.Context, id string) (claimed bool, err error)
}

// TenantStore backs TenantService — mints and looks up the B2B root
// credential a business integration authenticates as. See
// internal/domain/tenant's package doc.
type TenantStore interface {
	GetByTokenHash(ctx context.Context, hash string) (*tenant.Tenant, error)
	Get(ctx context.Context, id string) (*tenant.Tenant, error)
	Create(ctx context.Context, t *tenant.Tenant) error
	Revoke(ctx context.Context, id string, revokedAt time.Time) error
}

// PolicySetStore backs PolicySetService — persisted, versioned policy.Rules
// per tenant (and optionally per end user). See internal/domain/policyset.
type PolicySetStore interface {
	Create(ctx context.Context, ps *policyset.PolicySet) error
	GetActive(ctx context.Context, tenantID string, userID *string) (*policyset.PolicySet, error)
	SupersedeActive(ctx context.Context, tenantID string, userID *string, supersededAt time.Time) error
}

// PaymentIntentStore backs PaymentIntentService. See
// internal/domain/paymentintent.
type PaymentIntentStore interface {
	Create(ctx context.Context, p *paymentintent.AgenticPaymentIntent) error
	Get(ctx context.Context, id string) (*paymentintent.AgenticPaymentIntent, error)
	Update(ctx context.Context, p *paymentintent.AgenticPaymentIntent) error
	ListByTenant(ctx context.Context, tenantID string, limit int) ([]paymentintent.AgenticPaymentIntent, error)
}

// WebhookEndpointStore backs WebhookDispatchService — a tenant's own
// registered outbound-webhook destinations. See
// internal/domain/tenant.WebhookEndpoint.
type WebhookEndpointStore interface {
	Create(ctx context.Context, w *tenant.WebhookEndpoint) error
	ListActiveByTenant(ctx context.Context, tenantID string) ([]tenant.WebhookEndpoint, error)
}

type PaymentSourceStore interface {
	List(ctx context.Context, userID string) ([]payment.PaymentSource, error)
	GetByID(ctx context.Context, id string) (*payment.PaymentSource, error)
	GetByAlias(ctx context.Context, userID, alias string) (*payment.PaymentSource, error)
	Create(ctx context.Context, s *payment.PaymentSource) error
	Revoke(ctx context.Context, id string, revokedAt time.Time) error
}

type OrderStore interface {
	Create(ctx context.Context, o *order.Order) error
	Get(ctx context.Context, id string) (*order.Order, error)
	GetByIntent(ctx context.Context, intentID string) (*order.Order, error)
	UpdateStatus(ctx context.Context, orderID string, status order.Status) error
	AddEvent(ctx context.Context, e order.Event) error
	ListEvents(ctx context.Context, orderID string) ([]order.Event, error)
}

// SpendLedger answers "how much has this user already spent today" from
// authoritative order state — it is a read against real persisted orders,
// never a client-supplied number, which is what makes the daily-limit
// policy check tamper-proof.
type SpendLedger interface {
	SpendToday(ctx context.Context, userID, currency string, now time.Time) (int64, error)
}

// IdempotencyStore backs RunIdempotent (idempotency.go).
type IdempotencyStore interface {
	Begin(ctx context.Context, key, scope string) (existing []byte, alreadyDone bool, err error)
	Complete(ctx context.Context, key, scope string, response []byte) error
}

// Locker is an optional short-lived distributed lock (mandate §35), used to
// fail fast on a concurrent double-execution attempt rather than let two
// callers both do the work of refreshing a quote and re-running policy
// before Postgres's approvals.MarkConsumed sorts out the real winner. It is
// an efficiency layer, never the safety mechanism — every caller of a
// Locker still goes through the Postgres-backed compare-and-swap
// regardless of whether a lock was available. A nil Locker (Redis not
// configured) simply means every attempt proceeds to that real check
// directly — correct, just without the fast-fail optimization.
type Locker interface {
	Lock(ctx context.Context, key string, ttl time.Duration) (release func(context.Context), acquired bool, err error)
}

// PrivacyResolver is the app-layer view of internal/domain/privacy.Resolver
// (which satisfies it structurally). It exists so OrderService can turn the
// aliases an agent supplied ("shipping:home", "payment:personal") into the
// real values a merchant needs, at the last possible moment before the
// checkout call and nowhere else — see OrderService.Execute and
// merchant.Fulfillment.
type PrivacyResolver interface {
	ResolveShipping(ctx context.Context, userID, alias string, authz privacy.ResolveAuthorization) (*privacy.ShippingProfile, error)
	ResolveBilling(ctx context.Context, userID, alias string, authz privacy.ResolveAuthorization) (*privacy.BillingProfile, error)
}

// RateLimiter is an optional per-key fixed-window limiter (mandate §49). A
// nil RateLimiter (Redis not configured) means requests are not
// rate-limited at this layer — acceptable for local development, not for a
// production deployment (see docs/LOCAL_DEVELOPMENT.md).
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

// WebSearcher is an optional general web-search fallback (see
// connectors/websearch), used only when no merchant connector can find a
// product. It is not a merchant: no price, no cart, no order — just links.
// A nil WebSearcher (the default) means the capability is off.
type WebSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]websearch.Result, error)
}

// BankOfferSource supplies the operator-curated bank/card offers
// (internal/platform/bankoffers). Neither Amazon nor Flipkart publishes
// these through an API. Offers returns every offer on file, including
// expired ones — DiscoveryService.FindDeals filters by date.
type BankOfferSource interface {
	Offers(ctx context.Context) ([]deal.BankOffer, error)
}

// BudgetSearcher is a WebSearcher that can steer the search itself toward
// a price ceiling (connectors/websearch.Gemini). DiscoveryService filters
// by budget regardless; this just means the search spends its slots on
// options the user can actually afford instead of ones filtered out later.
type BudgetSearcher interface {
	SearchWithBudget(ctx context.Context, query string, limit int, maxPriceMinor int64) ([]websearch.Result, error)
}

// SearchCache is an optional short-lived cache for web-search results
// (internal/platform/redis.Client satisfies it). Web search costs money per
// query and takes seconds; the same product looked up by several users, or
// twice by one agent, should not pay that twice. A nil cache simply means
// every search goes out live.
type SearchCache interface {
	GetJSON(ctx context.Context, key string, out any) bool
	SetJSON(ctx context.Context, key string, v any, ttl time.Duration)
}

// ConnectorRegistry holds every configured MerchantConnector by name. It is
// a concrete type, not an interface — it's in-process wiring state owned by
// app, not a swappable backend.
type ConnectorRegistry struct {
	byName map[string]merchant.Connector
}

func NewConnectorRegistry() *ConnectorRegistry {
	return &ConnectorRegistry{byName: map[string]merchant.Connector{}}
}

func (r *ConnectorRegistry) Register(c merchant.Connector) {
	r.byName[c.Name()] = c
}

func (r *ConnectorRegistry) Get(name string) (merchant.Connector, error) {
	c, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: merchant connector %q not registered", shared.ErrNotFound, name)
	}
	return c, nil
}

func (r *ConnectorRegistry) List() []merchant.Connector {
	out := make([]merchant.Connector, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	return out
}
