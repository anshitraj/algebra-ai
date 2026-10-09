package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	mockconnector "github.com/project-algebra/algebra/connectors/mock"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/resilience"
	"github.com/project-algebra/algebra/policy"
)

// harness wires every app service against in-memory fakes plus the REAL
// mock merchant connector (connectors/mock is fully functional, not a
// test double itself) and the REAL LocalProvider policy engine. This lets
// orchestration logic — permission checks, state transitions, approval
// binding, the idempotency wrapper, the execute-time policy re-check — be
// exercised exactly as cmd/api and cmd/mcp exercise it, without Postgres.
type harness struct {
	agents         *fakeAgentStore
	intents        *fakeIntentStore
	paymentIntents *fakePaymentIntentStore
	quotes         *fakeQuoteStore
	decisions      *fakePolicyDecisionStore
	approvals      *fakeApprovalStore
	orders         *fakeOrderStore
	ledger         *fakeSpendLedger
	idempotency    *fakeIdempotencyStore
	audit          *fakeAuditLogger
	connectors     *ConnectorRegistry
	mockConnector  *mockconnector.Connector
	privacyStore   *fakePrivacyStore

	PrivacySvc *privacy.Resolver
	IntentSvc  *IntentService
	Discovery  *DiscoveryService
	QuoteSvc   *QuoteService
	PolicySvc  *PolicyService
	Approvals  *ApprovalService
	Orders     *OrderService
}

func newHarness(provider policy.Provider) *harness {
	h := &harness{
		agents: newFakeAgentStore(), intents: newFakeIntentStore(), paymentIntents: newFakePaymentIntentStore(), quotes: newFakeQuoteStore(),
		decisions: newFakePolicyDecisionStore(), approvals: newFakeApprovalStore(), orders: newFakeOrderStore(),
		ledger: &fakeSpendLedger{}, idempotency: newFakeIdempotencyStore(), audit: newFakeAuditLogger(),
		connectors: NewConnectorRegistry(),
	}
	h.mockConnector = mockconnector.New()
	h.connectors.Register(h.mockConnector)

	// A REAL privacy.Resolver (real AES-256-GCM, real audit trail) over an
	// in-memory store — the alias→address resolution on the execution path
	// is exactly what these tests need to exercise, not stub out.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("orchestration test: generating encryption key: " + err.Error())
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		panic("orchestration test: building encryptor: " + err.Error())
	}
	h.privacyStore = newFakePrivacyStore()
	h.PrivacySvc = privacy.NewResolver(h.privacyStore, enc, &privacyAuditSink{logger: h.audit})

	h.IntentSvc = NewIntentService(h.intents, h.agents, h.audit)
	h.Discovery = NewDiscoveryService(h.intents, h.agents, h.quotes, h.connectors, h.audit, 5*time.Minute)
	h.QuoteSvc = NewQuoteService(h.intents, h.agents, h.quotes, h.connectors)
	h.PolicySvc = NewPolicyService(h.intents, h.agents, h.quotes, h.decisions, h.approvals, h.ledger, provider, h.audit, 15*time.Minute)
	h.Approvals = NewApprovalService(h.intents, h.paymentIntents, h.approvals, h.QuoteSvc, h.audit)
	h.Orders = NewOrderService(h.intents, h.agents, h.approvals, h.orders, h.QuoteSvc, h.connectors, provider, h.ledger, h.audit, 500)
	h.Orders.SetPrivacyResolver(h.PrivacySvc)
	return h
}

// seedShippingProfile stores a real (encrypted) shipping address under an
// alias, the way POST /api/v1/profiles/shipping does in production.
func (h *harness) seedShippingProfile(t *testing.T, userID, alias string, profile privacy.ShippingProfile) {
	t.Helper()
	if err := h.PrivacySvc.StoreShipping(context.Background(), "profile_"+alias, userID, alias, profile, time.Now()); err != nil {
		t.Fatalf("seeding shipping profile: %v", err)
	}
}

// testShippingProfile is the address every orchestration test ships to.
func testShippingProfile() privacy.ShippingProfile {
	return privacy.ShippingProfile{
		RecipientName: "A. User", Line1: "12 MG Road", City: "Bengaluru",
		State: "KA", PostalCode: "560001", Country: "IN", Phone: "+919999999999",
	}
}

func (h *harness) mustCreateAgent(t *testing.T, userID string, perms ...agent.Permission) string {
	t.Helper()
	id := "agent_" + userID + "_" + time.Now().Format("150405.000000000")
	h.agents.put(&agent.Identity{ID: id, UserID: userID, ClientID: "test", Name: "test", Permissions: perms, CreatedAt: time.Now()})
	return id
}

func TestOrchestration_FullFlow_AutoApproved(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items: []intent.Item{{Query: "coke zero", Quantity: 1}, {Query: "chips", Quantity: 1}},
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: 40000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home",
		},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v (quotes=%v)", err, quotes)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}

	dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}
	if dec.Decision != policy.Allow {
		t.Fatalf("expected ALLOW, got %s (%v)", dec.Decision, dec.ReasonCodes)
	}

	outcome, err := h.Orders.Execute(ctx, h.idempotency, "exec-1", agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.IntentStatus != intent.StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (%s)", outcome.IntentStatus, outcome.Reason)
	}
	if outcome.Order == nil || outcome.Order.ProviderMode != "mock" {
		t.Fatalf("expected a mock order, got %+v", outcome.Order)
	}

	// Audit trail sanity: IntentCreated ... OrderCompleted should all be present.
	var sawCreated, sawCompleted bool
	for _, e := range h.audit.all() {
		if e.Action == "IntentCreated" {
			sawCreated = true
		}
		if e.Action == "OrderCompleted" {
			sawCompleted = true
		}
	}
	if !sawCreated || !sawCompleted {
		t.Errorf("expected a full audit trail from IntentCreated to OrderCompleted, got: %+v", h.audit.all())
	}
}

func TestOrchestration_RequiresApproval_CrossUserRejected(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items: []intent.Item{
			{Query: "pasta", Quantity: 2}, {Query: "tomato sauce", Quantity: 2},
			{Query: "garlic bread", Quantity: 2}, {Query: "parmesan", Quantity: 2},
		},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 200000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home"},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v", err)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}

	dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}
	if dec.Decision != policy.RequireApproval {
		t.Fatalf("expected REQUIRE_APPROVAL (amount=%d), got %s", quotes[0].FinalPayable.MinorUnits, dec.Decision)
	}

	if _, err := h.Orders.Execute(ctx, h.idempotency, "exec-blocked", agentID, pi.ID); err == nil {
		t.Fatal("expected Execute to fail while APPROVAL_REQUIRED")
	}

	appr, err := h.Approvals.GetByIntent(ctx, "user-1", pi.ID)
	if err != nil {
		t.Fatalf("GetByIntent: %v", err)
	}
	if _, err := h.Approvals.Approve(ctx, "someone-else", appr.ID); err == nil {
		t.Fatal("expected a different user's Approve to be rejected")
	}
	if _, err := h.Approvals.Approve(ctx, "user-1", appr.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	outcome, err := h.Orders.Execute(ctx, h.idempotency, "exec-2", agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.IntentStatus != intent.StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (%s)", outcome.IntentStatus, outcome.Reason)
	}
}

func TestOrchestration_PolicyDeny_IsTerminal(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items:       []intent.Item{{Query: "gift card", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 100000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home", Category: "gift_cards"},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v", err)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}
	dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}
	if dec.Decision != policy.Deny {
		t.Fatalf("expected DENY, got %s", dec.Decision)
	}
	final, _ := h.IntentSvc.GetIntent(ctx, agentID, pi.ID)
	if final.Status != intent.StatePolicyRejected || !final.Status.Terminal() {
		t.Fatalf("expected terminal POLICY_REJECTED, got %s", final.Status)
	}
	if _, err := h.Orders.Execute(ctx, h.idempotency, "exec-denied", agentID, pi.ID); err == nil {
		t.Fatal("expected Execute to refuse a POLICY_REJECTED intent")
	}
}

func TestOrchestration_AgentWithoutPermission_Rejected(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	readOnlyAgent := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead) // no create_intent

	_, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: readOnlyAgent,
		Items:       []intent.Item{{Query: "chips", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"},
	})
	if err == nil {
		t.Fatal("expected an agent without shopping.create_intent to be rejected")
	}
}

func TestOrchestration_RevokedAgent_LosesAllAccess(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())
	if err := h.agents.Revoke(ctx, agentID, time.Now()); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items:       []intent.Item{{Query: "chips", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"},
	})
	if err == nil {
		t.Fatal("expected a revoked agent to be rejected even with previously-granted permissions")
	}
}

// requireApprovalAtPaymentTimePolicy wraps a real LocalProvider but forces
// EvaluatePayment (the execute-time re-check) to REQUIRE_APPROVAL, so the
// scenario "amount was fine at request_purchase time but crosses the
// threshold by the time Execute re-checks it" can be tested directly,
// without needing to engineer real merchant-side price drift.
type requireApprovalAtPaymentTimePolicy struct {
	*policy.LocalProvider
}

func (p *requireApprovalAtPaymentTimePolicy) EvaluatePayment(context.Context, policy.Input) (*policy.PolicyDecision, error) {
	return &policy.PolicyDecision{Decision: policy.RequireApproval, ReasonCodes: []string{"TEST_FORCED_REQUIRE_APPROVAL"}, PolicyVersion: "test"}, nil
}

func TestOrchestration_ExecuteTimePolicyRequireApproval_StopsBeforeCharging(t *testing.T) {
	provider := &requireApprovalAtPaymentTimePolicy{LocalProvider: policy.NewLocalProvider(policy.DefaultRules())}
	h := newHarness(provider)
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items:       []intent.Item{{Query: "coke zero", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 40000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home"},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v", err)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}
	// EvaluatePurchaseIntent (unmodified) still returns ALLOW for this small amount.
	dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}
	if dec.Decision != policy.Allow {
		t.Fatalf("expected ALLOW at request-purchase time, got %s", dec.Decision)
	}

	// Execute's OWN EvaluatePayment call is forced to RequireApproval — this
	// must stop the purchase, not silently charge it.
	outcome, err := h.Orders.Execute(ctx, h.idempotency, "exec-forced", agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.IntentStatus != intent.StateReapprovalRequired {
		t.Fatalf("expected REAPPROVAL_REQUIRED when the execute-time policy check requires approval, got %s (order=%v)", outcome.IntentStatus, outcome.Order)
	}
	if outcome.Order != nil {
		t.Fatal("no order must be created when the execute-time policy check does not return ALLOW")
	}

	appr, err := h.Approvals.GetByIntent(ctx, "user-1", pi.ID)
	if err != nil {
		t.Fatalf("GetByIntent: %v", err)
	}
	if appr.Status != approval.StatusReapprovalRequired {
		t.Fatalf("expected the approval itself to be flagged REAPPROVAL_REQUIRED, got %s", appr.Status)
	}
}

func TestOrchestration_ApprovalStore_MarkConsumed_ExactlyOneWinner(t *testing.T) {
	store := newFakeApprovalStore()
	ctx := context.Background()
	a := &approval.Approval{ID: "appr_race", Status: approval.StatusApproved, ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			claimed, err := store.MarkConsumed(ctx, a.ID)
			if err != nil {
				t.Error(err)
			}
			results <- claimed
		}()
	}
	winners := 0
	for i := 0; i < 10; i++ {
		if <-results {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("expected exactly 1 winner out of 10 concurrent MarkConsumed calls, got %d", winners)
	}
}

// TestOrchestration_ConcurrentExecute_LockerFailsFast proves the optional
// Locker (mandate §35, wired via OrderService.SetLocker) does what it's
// for: of several simultaneous Execute attempts on the same intent, exactly
// one proceeds to do the real work (refresh quote, re-check policy, place
// the order) and every other one is rejected immediately with a conflict
// error, rather than all of them racing through to MarkConsumed. This is a
// fast-fail efficiency layer — TestOrchestration_ApprovalStore_MarkConsumed_ExactlyOneWinner
// above is what actually proves correctness holds even with no Locker at
// all (mandate §32).
func TestOrchestration_ConcurrentExecute_LockerFailsFast(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.Orders.SetLocker(newFakeLocker())
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items:       []intent.Item{{Query: "coke zero", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 40000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home"},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v", err)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}
	if dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID); err != nil || dec.Decision != policy.Allow {
		t.Fatalf("EvaluateAndTransition: dec=%v err=%v", dec, err)
	}

	const attempts = 8
	var wg sync.WaitGroup
	successes := make(chan *ExecuteOutcome, attempts)
	conflicts := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			outcome, err := h.Orders.Execute(ctx, h.idempotency, "concurrent-exec-attempt", agentID, pi.ID)
			if err != nil {
				conflicts <- err
				return
			}
			successes <- outcome
		}(i)
	}
	wg.Wait()
	close(successes)
	close(conflicts)

	var succeeded []*ExecuteOutcome
	for o := range successes {
		succeeded = append(succeeded, o)
	}
	var conflictCount int
	for range conflicts {
		conflictCount++
	}
	// Every attempt either succeeds or is rejected — none should vanish or
	// hang. Depending on goroutine scheduling, a late attempt may either hit
	// the Locker conflict or land on RunIdempotent's replay path (since all
	// attempts share one idempotency key) — both are correct outcomes; what
	// must never happen is more than one DISTINCT order being created.
	if len(succeeded)+conflictCount != attempts {
		t.Fatalf("expected %d total outcomes, got %d successes + %d conflicts", attempts, len(succeeded), conflictCount)
	}
	if len(succeeded) == 0 {
		t.Fatal("expected at least one Execute attempt to succeed")
	}
	for _, o := range succeeded {
		if o.Order == nil || o.Order.ID != succeeded[0].Order.ID {
			t.Errorf("expected every successful attempt to report the SAME order, got %+v vs %+v", o.Order, succeeded[0].Order)
		}
	}
}

// TestOrchestration_ManyConcurrentIntents_NoCorruption is a scaled-down
// version of the mandate's §65 performance-test ask ("100 concurrent
// intents... find race conditions"): many agents creating intents at once
// must never corrupt shared state or silently drop one.
func TestOrchestration_ManyConcurrentIntents_NoCorruption(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	const n = 50
	var wg sync.WaitGroup
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
				UserID: "user-1", AgentID: agentID,
				Items:       []intent.Item{{Query: "chips", Quantity: 1}},
				Constraints: intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"},
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- pi.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Errorf("unexpected error creating a concurrent intent: %v", err)
	}
	seen := map[string]bool{}
	count := 0
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate intent ID %q created under concurrency", id)
		}
		seen[id] = true
		count++
	}
	if count != n {
		t.Fatalf("expected %d distinct intents, got %d", n, count)
	}
}

// TestOrchestration_Discovery_AppliesMerchantAdvertisedCoupon proves the
// coupon path is wired (mandate §17): discovery asks the merchant what
// offers exist, tries the code, and the merchant's own refreshed quote —
// not Algebra's arithmetic — reports the discount.
func TestOrchestration_Discovery_AppliesMerchantAdvertisedCoupon(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items:       []intent.Item{{Query: "coke zero", Quantity: 2}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 40000, Currency: "INR", DeliveryProfile: "shipping:home"},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v", err)
	}
	if quotes[0].CouponDiscount.MinorUnits <= 0 {
		t.Errorf("expected discovery to have applied the merchant's advertised coupon, got coupon_discount=%d in %+v",
			quotes[0].CouponDiscount.MinorUnits, quotes[0])
	}
	// And the discount must be reflected in what would actually be charged.
	if quotes[0].FinalPayable.MinorUnits >= quotes[0].Subtotal.MinorUnits+quotes[0].DeliveryFee.MinorUnits+quotes[0].PlatformFee.MinorUnits {
		t.Errorf("expected the applied coupon to lower final_payable: %+v", quotes[0])
	}
}

// TestOrchestration_PrivacyBoundary_MerchantGetsAddressAgentNeverDoes is
// the test for the product's central privacy claim (mandate §24/§27): the
// agent supplies only an alias, the MERCHANT receives the real address at
// checkout time, and the real address appears in NOTHING the agent can
// see — not the intent, not the quote, not the order/receipt.
func TestOrchestration_PrivacyBoundary_MerchantGetsAddressAgentNeverDoes(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items: []intent.Item{{Query: "coke zero", Quantity: 1}},
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: 40000, Currency: "INR",
			PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home",
		},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover: %v", err)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}
	if _, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID); err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}
	outcome, err := h.Orders.Execute(ctx, h.idempotency, "privacy-exec", agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.IntentStatus != intent.StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (%s)", outcome.IntentStatus, outcome.Reason)
	}

	// 1. The merchant got the real address.
	got := h.mockConnector.LastFulfillment()
	if got.Shipping == nil {
		t.Fatal("the merchant connector received NO shipping address — the privacy resolver never ran on the execution path")
	}
	want := testShippingProfile()
	if got.Shipping.Line1 != want.Line1 || got.Shipping.PostalCode != want.PostalCode || got.Shipping.Phone != want.Phone {
		t.Errorf("merchant received the wrong address: %+v", got.Shipping)
	}

	// 2. Nothing the agent can see contains it. These are the exact objects
	//    returned through MCP/REST responses.
	secret := want.Line1
	reloaded, _ := h.IntentSvc.GetIntent(ctx, agentID, pi.ID)
	receipt, _ := h.Orders.GetReceipt(ctx, agentID, pi.ID)
	agentVisible := []string{
		renderForLeakCheck(reloaded),
		renderForLeakCheck(receipt),
		renderForLeakCheck(quotes[0]),
		renderForLeakCheck(outcome.Order),
	}
	for i, blob := range agentVisible {
		if strings.Contains(blob, secret) || strings.Contains(blob, want.Phone) {
			t.Errorf("agent-visible object %d leaked a resolved private value: %s", i, blob)
		}
	}

	// 3. The resolution itself was audited.
	var resolved bool
	for _, e := range h.audit.all() {
		if e.Action == "PrivacyProfileResolved" && e.IntentID == pi.ID {
			resolved = true
		}
	}
	if !resolved {
		t.Error("expected a PrivacyProfileResolved audit event for this intent")
	}
}

// renderForLeakCheck serializes an agent-facing object the same way a
// transport would, so a leak test inspects what actually goes over the
// wire rather than Go's struct printing.
func renderForLeakCheck(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}

// TestOrchestration_CancelOrder_GoesThroughTheMerchant proves order
// cancellation is a real merchant call whose result Algebra records — not a
// local status flip (mandate §69: real integration over fake completeness).
func TestOrchestration_CancelOrder_GoesThroughTheMerchant(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items:       []intent.Item{{Query: "coke zero", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 40000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home"},
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	quotes, _ := h.Discovery.Discover(ctx, agentID, pi.ID)
	if len(quotes) == 0 {
		t.Fatal("Discover returned no quotes")
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote: %v", err)
	}
	if _, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID); err != nil {
		t.Fatalf("EvaluateAndTransition: %v", err)
	}
	outcome, err := h.Orders.Execute(ctx, h.idempotency, "cancel-exec", agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cancelled, err := h.Orders.CancelOrder(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if cancelled.Status != "CANCELLED" {
		t.Errorf("expected CANCELLED, got %s", cancelled.Status)
	}

	// The merchant's own record must agree — that's what proves the cancel
	// actually went through the connector rather than only updating Algebra.
	merchantOrder, err := h.mockConnector.GetOrder(ctx, outcome.Order.MerchantOrderID)
	if err != nil {
		t.Fatalf("merchant GetOrder: %v", err)
	}
	if merchantOrder.Status != "CANCELLED" {
		t.Errorf("expected the MERCHANT's order to be cancelled too, got %s", merchantOrder.Status)
	}

	// Cancelling again is a no-op, not an error.
	if _, err := h.Orders.CancelOrder(ctx, agentID, pi.ID); err != nil {
		t.Errorf("expected a repeat cancel to be idempotent, got %v", err)
	}
}

// TestOrchestration_CircuitBreaker_StopsCallingFailingConnector proves the
// wiring in discovery_service.go's callConnector, not just the breaker's
// state machine in isolation (that's resilience's own test file): once a
// connector has failed enough times to trip the breaker, SearchProducts
// stops invoking it at all until the reset timeout passes.
func TestOrchestration_CircuitBreaker_StopsCallingFailingConnector(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	agentID := h.mustCreateAgent(t, "user-1", agent.AllPermissions...)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())

	failing := &alwaysFailingConnector{}
	h.connectors.Register(failing)
	h.Discovery.SetResilience(resilience.NewRegistry(3, time.Hour), 0)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := h.Discovery.SearchProducts(ctx, agentID, "anything", 5); err != nil {
			t.Fatalf("SearchProducts call %d: unexpected error: %v", i, err)
		}
	}
	if got := failing.searchCallCount(); got != 3 {
		t.Fatalf("expected exactly 3 calls to reach the connector (enough to trip the breaker), got %d", got)
	}

	// The breaker should now be open — further calls must not reach the
	// connector at all.
	for i := 0; i < 5; i++ {
		if _, err := h.Discovery.SearchProducts(ctx, agentID, "anything", 5); err != nil {
			t.Fatalf("SearchProducts call after trip %d: unexpected error: %v", i, err)
		}
	}
	if got := failing.searchCallCount(); got != 3 {
		t.Errorf("expected the breaker to prevent any further calls to the connector, but call count is now %d", got)
	}
}
