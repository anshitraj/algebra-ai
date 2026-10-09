package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/commerceprofile"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/paymentintent"
	"github.com/project-algebra/algebra/internal/domain/policyset"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// This file provides in-memory fakes for every app.*Store interface, so
// application-service orchestration (permission checks, state transitions,
// approval binding, idempotency, the execute-time policy re-check) can be
// unit-tested without Postgres. Concurrency-sensitive fakes (approvals,
// idempotency) implement the same atomicity contract their Postgres
// counterparts do (approval_repo.go / idempotency_repo.go) so a race test
// here is meaningful, not just a happy-path check.

type fakeIntentStore struct {
	mu   sync.Mutex
	data map[string]*intent.PurchaseIntent
}

func newFakeIntentStore() *fakeIntentStore {
	return &fakeIntentStore{data: map[string]*intent.PurchaseIntent{}}
}

func (f *fakeIntentStore) Create(_ context.Context, pi *intent.PurchaseIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *pi
	f.data[pi.ID] = &cp
	return nil
}
func (f *fakeIntentStore) Get(_ context.Context, id string) (*intent.PurchaseIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pi, ok := f.data[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *pi
	return &cp, nil
}
func (f *fakeIntentStore) Update(_ context.Context, pi *intent.PurchaseIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[pi.ID]; !ok {
		return shared.ErrNotFound
	}
	cp := *pi
	f.data[pi.ID] = &cp
	return nil
}

type fakeAgentStore struct {
	mu   sync.Mutex
	data map[string]*agent.Identity
}

func newFakeAgentStore() *fakeAgentStore { return &fakeAgentStore{data: map[string]*agent.Identity{}} }

func (f *fakeAgentStore) put(a *agent.Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[a.ID] = a
}
func (f *fakeAgentStore) Get(_ context.Context, id string) (*agent.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.data[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return a, nil
}
func (f *fakeAgentStore) GetByTokenHash(_ context.Context, hash string) (*agent.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.data {
		if a.TokenHash == hash {
			return a, nil
		}
	}
	return nil, shared.ErrNotFound
}
func (f *fakeAgentStore) Create(_ context.Context, a *agent.Identity) error {
	f.put(a)
	return nil
}
func (f *fakeAgentStore) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.data[id]
	if !ok {
		return shared.ErrNotFound
	}
	a.RevokedAt = &revokedAt
	return nil
}

type fakeQuoteStore struct {
	mu       sync.Mutex
	byID     map[string]*quote.CheckoutQuote
	byIntent map[string][]*quote.CheckoutQuote
}

func newFakeQuoteStore() *fakeQuoteStore {
	return &fakeQuoteStore{byID: map[string]*quote.CheckoutQuote{}, byIntent: map[string][]*quote.CheckoutQuote{}}
}
func (f *fakeQuoteStore) Save(_ context.Context, intentID string, q *quote.CheckoutQuote) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[q.QuoteID] = q
	f.byIntent[intentID] = append(f.byIntent[intentID], q)
	return nil
}
func (f *fakeQuoteStore) Get(_ context.Context, quoteID string) (*quote.CheckoutQuote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.byID[quoteID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return q, nil
}
func (f *fakeQuoteStore) ListByIntent(_ context.Context, intentID string) ([]*quote.CheckoutQuote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byIntent[intentID], nil
}

type fakePolicyDecisionStore struct {
	mu     sync.Mutex
	latest map[string]*policy.PolicyDecision
}

func newFakePolicyDecisionStore() *fakePolicyDecisionStore {
	return &fakePolicyDecisionStore{latest: map[string]*policy.PolicyDecision{}}
}
func (f *fakePolicyDecisionStore) Save(_ context.Context, intentID string, dec *policy.PolicyDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latest[intentID] = dec
	return nil
}
func (f *fakePolicyDecisionStore) GetLatestByIntent(_ context.Context, intentID string) (*policy.PolicyDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.latest[intentID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return d, nil
}

type fakeApprovalStore struct {
	mu              sync.Mutex
	byID            map[string]*approval.Approval
	byIntent        map[string]string // intentID -> approvalID (most recent)
	byPaymentIntent map[string]string // agenticPaymentIntentID -> approvalID (most recent)
}

func newFakeApprovalStore() *fakeApprovalStore {
	return &fakeApprovalStore{byID: map[string]*approval.Approval{}, byIntent: map[string]string{}, byPaymentIntent: map[string]string{}}
}
func (f *fakeApprovalStore) Create(_ context.Context, a *approval.Approval) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *a
	f.byID[a.ID] = &cp
	if a.IntentID != "" {
		f.byIntent[a.IntentID] = a.ID
	}
	if a.AgenticPaymentIntentID != "" {
		f.byPaymentIntent[a.AgenticPaymentIntentID] = a.ID
	}
	return nil
}
func (f *fakeApprovalStore) Get(_ context.Context, id string) (*approval.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *a
	return &cp, nil
}
func (f *fakeApprovalStore) GetByPaymentIntent(_ context.Context, paymentIntentID string) (*approval.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byPaymentIntent[paymentIntentID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *f.byID[id]
	return &cp, nil
}
func (f *fakeApprovalStore) GetByIntent(_ context.Context, intentID string) (*approval.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byIntent[intentID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *f.byID[id]
	return &cp, nil
}
func (f *fakeApprovalStore) Update(_ context.Context, a *approval.Approval) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byID[a.ID]; !ok {
		return shared.ErrNotFound
	}
	cp := *a
	f.byID[a.ID] = &cp
	return nil
}

// MarkConsumed replicates approval_repo.go's atomic
// UPDATE ... WHERE status = 'APPROVED' semantics under a mutex, so a
// concurrency test against this fake is testing the same contract the real
// Postgres implementation promises.
func (f *fakeApprovalStore) MarkConsumed(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return false, shared.ErrNotFound
	}
	if a.Status != approval.StatusApproved {
		return false, nil
	}
	a.Status = approval.StatusConsumed
	return true, nil
}

// fakePaymentIntentStore is PaymentIntentStore's in-memory fake — same
// copy-on-read/write discipline as the other fakes in this file.
type fakePaymentIntentStore struct {
	mu   sync.Mutex
	data map[string]*paymentintent.AgenticPaymentIntent
}

func newFakePaymentIntentStore() *fakePaymentIntentStore {
	return &fakePaymentIntentStore{data: map[string]*paymentintent.AgenticPaymentIntent{}}
}
func (f *fakePaymentIntentStore) Create(_ context.Context, p *paymentintent.AgenticPaymentIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *p
	f.data[p.ID] = &cp
	return nil
}
func (f *fakePaymentIntentStore) Get(_ context.Context, id string) (*paymentintent.AgenticPaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.data[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *p
	return &cp, nil
}
func (f *fakePaymentIntentStore) Update(_ context.Context, p *paymentintent.AgenticPaymentIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[p.ID]; !ok {
		return shared.ErrNotFound
	}
	cp := *p
	f.data[p.ID] = &cp
	return nil
}
func (f *fakePaymentIntentStore) ListByTenant(_ context.Context, tenantID string, limit int) ([]paymentintent.AgenticPaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []paymentintent.AgenticPaymentIntent
	for _, p := range f.data {
		if p.TenantID == tenantID {
			out = append(out, *p)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type fakeUserStore struct {
	mu   sync.Mutex
	data map[string]*UserRecord
}

func newFakeUserStore() *fakeUserStore { return &fakeUserStore{data: map[string]*UserRecord{}} }

func (f *fakeUserStore) Create(_ context.Context, id, email, tenantID string, createdAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[id] = &UserRecord{ID: id, Email: email, TenantID: tenantID, CreatedAt: createdAt}
	return nil
}
func (f *fakeUserStore) Get(_ context.Context, id string) (*UserRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.data[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// fakePolicySetStore does not preserve superseded versions the way
// postgres.PolicySetRepo does (it overwrites in place) — fine for unit
// tests, which only need "the effective policy updates after SetPolicy",
// not version history.
type fakePolicySetStore struct {
	mu   sync.Mutex
	data map[string]*policyset.PolicySet
}

func newFakePolicySetStore() *fakePolicySetStore {
	return &fakePolicySetStore{data: map[string]*policyset.PolicySet{}}
}
func (f *fakePolicySetStore) key(tenantID string, userID *string) string {
	u := ""
	if userID != nil {
		u = *userID
	}
	return tenantID + "|" + u
}
func (f *fakePolicySetStore) Create(_ context.Context, ps *policyset.PolicySet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *ps
	f.data[f.key(ps.TenantID, ps.UserID)] = &cp
	return nil
}
func (f *fakePolicySetStore) GetActive(_ context.Context, tenantID string, userID *string) (*policyset.PolicySet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ps, ok := f.data[f.key(tenantID, userID)]
	if !ok || ps.SupersededAt != nil {
		return nil, shared.ErrNotFound
	}
	cp := *ps
	return &cp, nil
}
func (f *fakePolicySetStore) SupersedeActive(_ context.Context, tenantID string, userID *string, supersededAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ps, ok := f.data[f.key(tenantID, userID)]; ok {
		ps.SupersededAt = &supersededAt
	}
	return nil
}

type fakeCommerceProfileStore struct {
	mu   sync.Mutex
	data map[string]*commerceprofile.CommerceProfile
}

func newFakeCommerceProfileStore() *fakeCommerceProfileStore {
	return &fakeCommerceProfileStore{data: map[string]*commerceprofile.CommerceProfile{}}
}
func (f *fakeCommerceProfileStore) Get(_ context.Context, userID string) (*commerceprofile.CommerceProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.data[userID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *p
	return &cp, nil
}
func (f *fakeCommerceProfileStore) Upsert(_ context.Context, p *commerceprofile.CommerceProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *p
	f.data[p.UserID] = &cp
	return nil
}

type fakeOrderStore struct {
	mu       sync.Mutex
	byID     map[string]*order.Order
	byIntent map[string]*order.Order
	events   []order.Event
}

func newFakeOrderStore() *fakeOrderStore {
	return &fakeOrderStore{byID: map[string]*order.Order{}, byIntent: map[string]*order.Order{}}
}
func (f *fakeOrderStore) Create(_ context.Context, o *order.Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *o
	f.byID[o.ID] = &cp
	f.byIntent[o.IntentID] = &cp
	return nil
}
func (f *fakeOrderStore) Get(_ context.Context, id string) (*order.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return o, nil
}
func (f *fakeOrderStore) GetByIntent(_ context.Context, intentID string) (*order.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byIntent[intentID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return o, nil
}
func (f *fakeOrderStore) UpdateStatus(_ context.Context, orderID string, status order.Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[orderID]
	if !ok {
		return shared.ErrNotFound
	}
	o.Status = status
	return nil
}

func (f *fakeOrderStore) AddEvent(_ context.Context, e order.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeOrderStore) ListEvents(_ context.Context, orderID string) ([]order.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []order.Event
	for _, e := range f.events {
		if e.OrderID == orderID {
			out = append(out, e)
		}
	}
	return out, nil
}

// fakeSpendLedger returns a fixed amount regardless of arguments — tests
// that care about the daily-limit interaction set .amount directly.
type fakeSpendLedger struct{ amount int64 }

func (f *fakeSpendLedger) SpendToday(context.Context, string, string, time.Time) (int64, error) {
	return f.amount, nil
}

// fakeIdempotencyStore replicates idempotency_repo.go's Begin/Complete
// contract (first caller reserves, later callers with the same key replay
// the stored response) under a mutex.
type fakeIdempotencyStore struct {
	mu    sync.Mutex
	state map[string]idemEntry
}

type idemEntry struct {
	done     bool
	response []byte
}

func newFakeIdempotencyStore() *fakeIdempotencyStore {
	return &fakeIdempotencyStore{state: map[string]idemEntry{}}
}
func (f *fakeIdempotencyStore) key(k, scope string) string { return scope + "|" + k }

func (f *fakeIdempotencyStore) Begin(_ context.Context, k, scope string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(k, scope)
	entry, exists := f.state[key]
	if !exists {
		f.state[key] = idemEntry{}
		return nil, false, nil
	}
	if entry.done {
		return entry.response, true, nil
	}
	return nil, false, nil
}
func (f *fakeIdempotencyStore) Complete(_ context.Context, k, scope string, response []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[f.key(k, scope)] = idemEntry{done: true, response: response}
	return nil
}

// fakeLocker replicates redis.Client.Lock's semantics (first caller for a
// key wins until released or expired) with a plain mutex-guarded map, so
// OrderService's fail-fast concurrency path is testable without Redis.
type fakeLocker struct {
	mu   sync.Mutex
	held map[string]bool
}

func newFakeLocker() *fakeLocker { return &fakeLocker{held: map[string]bool{}} }

func (f *fakeLocker) Lock(_ context.Context, key string, _ time.Duration) (func(context.Context), bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[key] {
		return nil, false, nil
	}
	f.held[key] = true
	release := func(context.Context) {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.held, key)
	}
	return release, true, nil
}

// fakeAuditLogger records every event so tests can assert on the audit
// trail directly instead of trusting side effects happened silently.
type fakeAuditLogger struct {
	mu     sync.Mutex
	events []audit.Event
}

func newFakeAuditLogger() *fakeAuditLogger { return &fakeAuditLogger{} }

func (f *fakeAuditLogger) Record(_ context.Context, evt audit.Event) error {
	if err := evt.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, evt)
	return nil
}
func (f *fakeAuditLogger) all() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]audit.Event, len(f.events))
	copy(out, f.events)
	return out
}

// alwaysFailingConnector implements merchant.Connector with every method
// erroring except SearchProducts, whose call count is tracked — used to
// prove DiscoveryService's circuit breaker actually stops calling a
// misbehaving connector rather than just existing in isolation
// (internal/platform/resilience's own tests cover the breaker's state
// machine; this one covers the wiring).
type alwaysFailingConnector struct {
	mu          sync.Mutex
	searchCalls int
}

func (c *alwaysFailingConnector) searchCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.searchCalls
}

func (c *alwaysFailingConnector) Name() string                { return "always-failing" }
func (c *alwaysFailingConnector) Mode() merchant.ProviderMode { return merchant.ProviderModeMock }
func (c *alwaysFailingConnector) Capabilities() merchant.Capabilities {
	return merchant.Capabilities{Search: true}
}
func (c *alwaysFailingConnector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) SearchProducts(context.Context, string, int) ([]merchant.Product, error) {
	c.mu.Lock()
	c.searchCalls++
	c.mu.Unlock()
	return nil, fmt.Errorf("always-failing: simulated failure")
}
func (c *alwaysFailingConnector) GetProduct(context.Context, string) (*merchant.Product, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) GetOffers(context.Context, string) ([]quote.Offer, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) CreateCart(context.Context, string) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) AddToCart(context.Context, string, string, int) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) RemoveFromCart(context.Context, string, string) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) GetDeliveryOptions(context.Context, string, string) ([]merchant.DeliveryOption, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) ApplyCoupon(context.Context, string, string) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) GetCheckoutQuote(context.Context, string) (*quote.CheckoutQuote, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) ExecuteCheckout(context.Context, string, string, merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) GetOrder(context.Context, string) (*order.Order, error) {
	return nil, shared.ErrNotImplemented
}
func (c *alwaysFailingConnector) CancelOrder(context.Context, string) error {
	return shared.ErrNotImplemented
}

var _ merchant.Connector = (*alwaysFailingConnector)(nil)

// fakePrivacyStore is an in-memory privacy.Store so tests can exercise the
// REAL privacy.Resolver (real AES-256-GCM, real alias→address resolution,
// real audit trail) rather than a stubbed-out resolver — the point of these
// tests is that the resolution actually happens on the execution path.
type fakePrivacyStore struct {
	mu   sync.Mutex
	data map[string]*privacy.StoredProfile
}

func newFakePrivacyStore() *fakePrivacyStore {
	return &fakePrivacyStore{data: map[string]*privacy.StoredProfile{}}
}

func (f *fakePrivacyStore) key(userID, alias string) string { return userID + "|" + alias }

func (f *fakePrivacyStore) Get(_ context.Context, userID, alias string) (*privacy.StoredProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.data[f.key(userID, alias)]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return p, nil
}

func (f *fakePrivacyStore) Put(_ context.Context, profile *privacy.StoredProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[f.key(profile.UserID, profile.Alias)] = profile
	return nil
}

func (f *fakePrivacyStore) ListAliases(_ context.Context, userID string, t privacy.ProfileType) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.data {
		if p.UserID == userID && p.Type == t {
			out = append(out, p.Alias)
		}
	}
	return out, nil
}

// privacyAuditSink adapts the fake audit logger to privacy.AuditSink so
// every resolution is recorded, exactly as postgres.ResolutionAuditSink
// does in production.
type privacyAuditSink struct{ logger *fakeAuditLogger }

func (s *privacyAuditSink) RecordResolution(ctx context.Context, userID, alias string, authz privacy.ResolveAuthorization) error {
	evt := audit.NewEvent("PrivacyProfileResolved", time.Now())
	evt.UserID = userID
	evt.AgentID = authz.RequestedBy
	evt.IntentID = authz.IntentID
	evt.Result = "resolved:" + alias + " purpose:" + authz.Purpose
	return s.logger.Record(ctx, evt)
}
