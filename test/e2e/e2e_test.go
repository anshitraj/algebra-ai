// Package e2e exercises the complete sandbox commerce flow the mandate's
// own definition of a good MVP describes (§62): prompt → intent →
// discovery → quote → policy → approval → checkout → receipt, with a
// complete audit trail — against real Postgres and the deterministic mock
// merchant connector. It needs `make dev-up` first; see
// docs/LOCAL_DEVELOPMENT.md. Every test skips cleanly if DATABASE_URL /
// ALGEBRA_MASTER_KEY aren't set, so `make test` alone never requires
// Docker.
package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/wiring"
	"github.com/project-algebra/algebra/policy"
)

func requireBundle(t *testing.T) *wiring.Bundle {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("ALGEBRA_MASTER_KEY") == "" {
		t.Skip("DATABASE_URL / ALGEBRA_MASTER_KEY not set; skipping E2E test (see docs/LOCAL_DEVELOPMENT.md)")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	bundle, err := wiring.Build(context.Background(), cfg, "../../migrations")
	if err != nil {
		t.Fatalf("building application: %v", err)
	}
	t.Cleanup(bundle.DB.Close)
	// These tests shop at the mock store, which accounts in live or demo mode
	// are never offered; with no account modes it is open to every test user.
	bundle.Discovery.SetAccountModes(nil)
	return bundle
}

func mustBootstrapUserAndAgent(t *testing.T, b *wiring.Bundle) (userID, agentID string) {
	t.Helper()
	ctx := context.Background()
	user, err := b.Users.Create(ctx, "e2e-"+uuid.NewString()+"@example.test", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	_, identity, err := b.AgentSvc.CreateAgent(ctx, user.ID, "e2e-test-client", "e2e-test-agent", agent.AllPermissions)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	return user.ID, identity.ID
}

// mustSeedShipping stores a real (encrypted) shipping address under the
// "shipping:home" alias every test intent below uses. Without this,
// OrderService.resolveFulfillment has nothing to resolve and the mock
// connector correctly refuses to check out an undeliverable order — see
// docs/PRIVACY.md: the merchant call is the only place a resolved address
// exists, and it only exists if a profile was ever stored for the alias.
func mustSeedShipping(t *testing.T, b *wiring.Bundle, userID string) {
	t.Helper()
	profile := privacy.ShippingProfile{
		RecipientName: "E2E Test User", Line1: "1 Test St", City: "Bengaluru",
		State: "KA", PostalCode: "560001", Country: "IN", Phone: "+910000000000",
	}
	if err := b.Privacy.StoreShipping(context.Background(), "profile_"+uuid.NewString(), userID, "shipping:home", profile, time.Now()); err != nil {
		t.Fatalf("seeding shipping profile: %v", err)
	}
}

// TestFullFlow_AutoApprovedUnderThreshold covers the mandate's MVP example
// almost verbatim: order groceries under a policy-set threshold, with no
// human click required, end to end through a real (mock) merchant.
func TestFullFlow_AutoApprovedUnderThreshold(t *testing.T) {
	b := requireBundle(t)
	ctx := context.Background()
	userID, agentID := mustBootstrapUserAndAgent(t, b)
	mustSeedShipping(t, b, userID)

	pi, err := b.Intents.CreateIntent(ctx, b.Idempotency, "", app.CreateIntentInput{
		UserID: userID, AgentID: agentID,
		Items: []intent.Item{{Query: "coke zero", Quantity: 1}, {Query: "chips", Quantity: 1}},
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: 40000, Currency: "INR",
			PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home",
		},
	})
	if err != nil {
		t.Fatalf("CreateIntent failed: %v", err)
	}
	if pi.Status != intent.StateDraft {
		t.Fatalf("expected DRAFT, got %s", pi.Status)
	}

	quotes, err := b.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(quotes) == 0 {
		t.Fatal("expected at least one quote from the mock connector")
	}
	mockQuote := quotes[0]
	if mockQuote.Merchant != "mock" {
		t.Fatalf("expected the mock connector's quote, got merchant=%s", mockQuote.Merchant)
	}

	if err := b.Quotes.SelectQuote(ctx, agentID, pi.ID, mockQuote.QuoteID); err != nil {
		t.Fatalf("SelectQuote failed: %v", err)
	}

	dec, err := b.Policy.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition failed: %v", err)
	}
	if dec.Decision != policy.Allow {
		t.Fatalf("expected ALLOW for a small, in-policy order, got %s (%v)", dec.Decision, dec.ReasonCodes)
	}

	afterPolicy, err := b.Intents.GetIntent(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("GetIntent failed: %v", err)
	}
	if afterPolicy.Status != intent.StateApproved {
		t.Fatalf("expected APPROVED after an ALLOW decision, got %s", afterPolicy.Status)
	}

	idemKey := "e2e-exec-" + uuid.NewString()
	outcome, err := b.Orders.Execute(ctx, b.Idempotency, idemKey, agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if outcome.IntentStatus != intent.StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (reason: %s)", outcome.IntentStatus, outcome.Reason)
	}
	if outcome.Order == nil || outcome.Order.ProviderMode != "mock" {
		t.Fatalf("expected a mock-mode order, got %+v", outcome.Order)
	}

	receipt, err := b.Orders.GetReceipt(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("GetReceipt failed: %v", err)
	}
	if receipt.MerchantOrderID == "" {
		t.Error("expected a non-empty merchant order ID on the receipt")
	}

	// Idempotency: retrying the EXACT SAME request (same idempotency key)
	// must replay the original result rather than re-running Execute —
	// which would otherwise fail outright since the intent is no longer
	// APPROVED, let alone create a second order (mandate §31).
	replay, err := b.Orders.Execute(ctx, b.Idempotency, idemKey, agentID, pi.ID)
	if err != nil {
		t.Fatalf("replayed Execute call failed instead of returning the cached result: %v", err)
	}
	if replay.Order == nil || replay.Order.ID != outcome.Order.ID {
		t.Errorf("expected the replay to return the same order %s, got %+v", outcome.Order.ID, replay.Order)
	}
}

// TestFullFlow_RequiresHumanApproval covers a purchase large enough to
// cross the policy's approval threshold: the agent cannot execute until a
// human calls the exact endpoint a real Approval UI would (ApprovalService,
// keyed by userID, never by agentID).
func TestFullFlow_RequiresHumanApproval(t *testing.T) {
	b := requireBundle(t)
	ctx := context.Background()
	userID, agentID := mustBootstrapUserAndAgent(t, b)
	mustSeedShipping(t, b, userID)

	// 2x of everything non-gift-card in the mock catalog comfortably clears
	// the ₹1,000 approval threshold while staying under the ₹2,000
	// per-transaction limit.
	pi, err := b.Intents.CreateIntent(ctx, b.Idempotency, "", app.CreateIntentInput{
		UserID: userID, AgentID: agentID,
		Items: []intent.Item{
			{Query: "pasta", Quantity: 2}, {Query: "tomato sauce", Quantity: 2},
			{Query: "garlic bread", Quantity: 2}, {Query: "parmesan", Quantity: 2},
		},
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: 200000, Currency: "INR",
			PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home",
		},
	})
	if err != nil {
		t.Fatalf("CreateIntent failed: %v", err)
	}

	quotes, err := b.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover failed: %v (quotes=%v)", err, quotes)
	}
	if err := b.Quotes.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote failed: %v", err)
	}

	preview, err := b.Policy.PreviewDecision(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("PreviewDecision failed: %v", err)
	}
	if preview.Decision != policy.RequireApproval {
		t.Fatalf("expected this order to require approval (final_payable=%d), got %s", quotes[0].FinalPayable.MinorUnits, preview.Decision)
	}

	dec, err := b.Policy.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition failed: %v", err)
	}
	if dec.Decision != policy.RequireApproval {
		t.Fatalf("expected REQUIRE_APPROVAL, got %s", dec.Decision)
	}

	afterPolicy, err := b.Intents.GetIntent(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("GetIntent failed: %v", err)
	}
	if afterPolicy.Status != intent.StateApprovalRequired {
		t.Fatalf("expected APPROVAL_REQUIRED, got %s", afterPolicy.Status)
	}

	// The agent must NOT be able to execute yet.
	if _, err := b.Orders.Execute(ctx, b.Idempotency, "e2e-blocked-"+uuid.NewString(), agentID, pi.ID); err == nil {
		t.Fatal("expected Execute to fail while the intent is still APPROVAL_REQUIRED")
	}

	appr, err := b.Approvals.GetByIntent(ctx, userID, pi.ID)
	if err != nil {
		t.Fatalf("GetByIntent failed: %v", err)
	}
	if appr.Status != "PENDING" {
		t.Fatalf("expected a PENDING approval, got %s", appr.Status)
	}

	// A different user must not be able to approve someone else's approval.
	otherUser, _ := mustBootstrapUserAndAgent(t, b)
	if _, err := b.Approvals.Approve(ctx, otherUser, appr.ID); err == nil {
		t.Fatal("expected a different user's Approve call to be rejected")
	}

	approved, err := b.Approvals.Approve(ctx, userID, appr.ID)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if approved.Status != "APPROVED" {
		t.Fatalf("expected APPROVED, got %s", approved.Status)
	}

	afterApproval, err := b.Intents.GetIntent(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("GetIntent failed: %v", err)
	}
	if afterApproval.Status != intent.StateApproved {
		t.Fatalf("expected APPROVED, got %s", afterApproval.Status)
	}

	outcome, err := b.Orders.Execute(ctx, b.Idempotency, "e2e-exec-"+uuid.NewString(), agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if outcome.IntentStatus != intent.StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s (reason: %s)", outcome.IntentStatus, outcome.Reason)
	}
}

// TestPolicyDeny_BlockedCategoryNeverReachesApproval proves a DENY is
// terminal — a gift card purchase must never reach APPROVAL_REQUIRED or
// APPROVED no matter what, per mandate §8: "Never let an LLM override a
// DENY result."
func TestPolicyDeny_BlockedCategoryNeverReachesApproval(t *testing.T) {
	b := requireBundle(t)
	ctx := context.Background()
	userID, agentID := mustBootstrapUserAndAgent(t, b)

	pi, err := b.Intents.CreateIntent(ctx, b.Idempotency, "", app.CreateIntentInput{
		UserID: userID, AgentID: agentID,
		Items:       []intent.Item{{Query: "gift card", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 100000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home", Category: "gift_cards"},
	})
	if err != nil {
		t.Fatalf("CreateIntent failed: %v", err)
	}
	quotes, err := b.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("Discover failed: %v", err)
	}
	if err := b.Quotes.SelectQuote(ctx, agentID, pi.ID, quotes[0].QuoteID); err != nil {
		t.Fatalf("SelectQuote failed: %v", err)
	}

	dec, err := b.Policy.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("EvaluateAndTransition failed: %v", err)
	}
	if dec.Decision != policy.Deny {
		t.Fatalf("expected DENY for a gift-card purchase, got %s", dec.Decision)
	}

	final, err := b.Intents.GetIntent(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatalf("GetIntent failed: %v", err)
	}
	if final.Status != intent.StatePolicyRejected {
		t.Fatalf("expected POLICY_REJECTED, got %s", final.Status)
	}
	if !final.Status.Terminal() {
		t.Fatal("POLICY_REJECTED must be terminal")
	}
	if _, err := b.Orders.Execute(ctx, b.Idempotency, "e2e-denied-"+uuid.NewString(), agentID, pi.ID); err == nil {
		t.Fatal("expected Execute to refuse a POLICY_REJECTED intent")
	}
}
