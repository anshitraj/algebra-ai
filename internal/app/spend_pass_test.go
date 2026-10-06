package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/policy"
)

// --- fakes ---

type fakePassStore struct {
	mu     sync.Mutex
	passes map[string]*spendpass.Pass
}

func newFakePassStore() *fakePassStore { return &fakePassStore{passes: map[string]*spendpass.Pass{}} }

func (f *fakePassStore) Create(_ context.Context, p *spendpass.Pass) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *p
	f.passes[p.ID] = &cp
	return nil
}

func (f *fakePassStore) Get(_ context.Context, id string) (*spendpass.Pass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.passes[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, shared.ErrNotFound
}

func (f *fakePassStore) GetByAgent(_ context.Context, agentID string) (*spendpass.Pass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.passes {
		if p.AgentID == agentID {
			cp := *p
			return &cp, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (f *fakePassStore) ListByUser(_ context.Context, userID string) ([]spendpass.Pass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []spendpass.Pass
	for _, p := range f.passes {
		if p.UserID == userID {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakePassStore) Revoke(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.passes[id]; ok && p.RevokedAt == nil {
		p.RevokedAt = &at
	}
	return nil
}

func (f *fakeOrderStore) all() []order.Order {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]order.Order, 0, len(f.byID))
	for _, o := range f.byID {
		out = append(out, *o)
	}
	return out
}

// fakePassLedger sums the harness's own orders by the ordering agent, like
// the Postgres ledger's join on purchase_intents.agent_id.
type fakePassLedger struct{ h *harness }

func (l fakePassLedger) SpentByAgentSince(ctx context.Context, agentID, currency string, since time.Time) (int64, error) {
	var total int64
	for _, o := range l.h.orders.all() {
		pi, err := l.h.intents.Get(ctx, o.IntentID)
		if err == nil && pi.AgentID == agentID && o.Total.Currency == currency && !o.PlacedAt.Before(since) {
			total += o.Total.MinorUnits
		}
	}
	return total, nil
}

type fakeReceiptStore struct {
	mu   sync.Mutex
	byID map[string]*StoredReceipt
}

func (f *fakeReceiptStore) Create(_ context.Context, r *StoredReceipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byID == nil {
		f.byID = map[string]*StoredReceipt{}
	}
	cp := *r
	f.byID[r.ID] = &cp
	return nil
}

func (f *fakeReceiptStore) Get(_ context.Context, id string) (*StoredReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.byID[id]; ok {
		return r, nil
	}
	return nil, shared.ErrNotFound
}

func (f *fakeReceiptStore) GetByOrder(_ context.Context, orderID string) (*StoredReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.byID {
		if r.OrderID == orderID {
			return r, nil
		}
	}
	return nil, shared.ErrNotFound
}

type passHarness struct {
	*harness
	passes   *SpendPassService
	receipts *ReceiptService
	store    *fakePassStore
}

func newPassHarness(t *testing.T) *passHarness {
	t.Helper()
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	store := newFakePassStore()
	passes := NewSpendPassService(store, NewAgentService(h.agents), fakePassLedger{h})
	h.PolicySvc.SetSpendPasses(passes)
	h.Orders.SetSpendPasses(passes)
	signer, err := receipt.NewSigner(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	receipts := NewReceiptService(signer, "https://algebra.test", &fakeReceiptStore{}, h.agents, h.decisions, store)
	h.Orders.SetReceipts(receipts)
	h.seedShippingProfile(t, "user-1", "shipping:home", testShippingProfile())
	return &passHarness{harness: h, passes: passes, receipts: receipts, store: store}
}

func i64p(v int64) *int64 { return &v }

// buy runs the golden path for "coke zero" (₹85 with fees at the mock store)
// as agentID and returns the policy decision and, if it went through, the
// execution outcome.
func (h *passHarness) buy(t *testing.T, agentID, category string) (*policy.PolicyDecision, *ExecuteOutcome) {
	t.Helper()
	ctx := context.Background()
	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: agentID,
		Items: []intent.Item{{Query: "coke zero", Quantity: 1}},
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: 40000, Currency: "INR", Category: category, PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home",
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
		t.Fatal(err)
	}
	dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Decision != policy.Allow {
		return dec, nil
	}
	out, err := h.Orders.Execute(ctx, h.idempotency, "exec-"+pi.ID, agentID, pi.ID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return dec, out
}

func TestSpendPass_PurchaseGetsAVerifiableReceipt(t *testing.T) {
	h := newPassHarness(t)
	ctx := context.Background()
	issued, err := h.passes.Create(ctx, "user-1", spendpass.Pass{
		Label: "Claude — snacks", AgentKind: spendpass.AgentClaude, BudgetMinorUnits: 20000, BudgetPeriod: spendpass.PeriodTotal,
		AllowedCategories: []string{"groceries"}, ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.RemainingMinor != 20000 {
		t.Fatalf("unexpected issued pass %+v", issued)
	}

	dec, out := h.buy(t, issued.AgentID, "groceries")
	if dec.Decision != policy.Allow || out == nil || out.IntentStatus != intent.StateSucceeded {
		t.Fatalf("a purchase inside the pass should go through: %+v %+v", dec, out)
	}
	if !slices.Contains(dec.ReasonCodes, spendpass.ReasonOK) || !strings.Contains(dec.PolicyVersion, "+pass:"+issued.ID) {
		t.Fatalf("the decision should show the pass was checked: %+v", dec)
	}
	if out.Receipt == "" {
		t.Fatal("a placed order must come with a signed receipt")
	}

	v := h.receipts.Verify(ctx, out.Receipt)
	if !v.Valid || !v.Recorded || v.Claims.Pass != issued.ID || v.Claims.Amount.MinorUnits != out.Order.Total.MinorUnits {
		t.Fatalf("receipt should verify and describe the purchase: %+v", v)
	}
	if v.Claims.Authorization.Method != receipt.MethodPolicy || v.Claims.Agent.Client != "spend-pass:claude" || !v.Claims.Test {
		t.Fatalf("receipt should say policy approved it, for the pass's agent, as a test order: %+v", v.Claims)
	}
	if strings.Contains(out.Receipt, "user-1") {
		t.Fatal("a receipt must not carry the user ID")
	}
	if v.Pass == nil || !v.Pass.Active {
		t.Fatalf("verification should report the pass as active: %+v", v.Pass)
	}

	// The order page shows the same receipt.
	detail, err := h.Orders.OrderForUser(ctx, "user-1", out.Order.ID)
	if err != nil || detail.Receipt != out.Receipt {
		t.Fatalf("order detail should carry the receipt: %v", err)
	}
	// Tampering breaks it.
	if bad := h.receipts.Verify(ctx, out.Receipt[:len(out.Receipt)-4]+"AAAA"); bad.Valid {
		t.Fatal("a tampered receipt must not verify")
	}

	// What's left is visible to the agent and the person.
	view, err := h.passes.ForAgent(ctx, issued.AgentID)
	if err != nil || view.SpentMinorUnits != out.Order.Total.MinorUnits || view.RemainingMinor != 20000-out.Order.Total.MinorUnits {
		t.Fatalf("budget accounting wrong: %+v %v", view, err)
	}
}

func TestSpendPass_RefusesWhatThePassDoesntAllow(t *testing.T) {
	h := newPassHarness(t)
	ctx := context.Background()
	issued, err := h.passes.Create(ctx, "user-1", spendpass.Pass{
		Label: "Groceries only", BudgetMinorUnits: 10000, AllowedCategories: []string{"groceries"}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec, _ := h.buy(t, issued.AgentID, "electronics"); dec.Decision != policy.Deny || !slices.Contains(dec.ReasonCodes, spendpass.ReasonCategoryNotAllowed) {
		t.Fatalf("a category outside the pass must be denied: %+v", dec)
	}
	// ₹85 fits the ₹100 budget once; the second ₹85 doesn't.
	if dec, out := h.buy(t, issued.AgentID, "groceries"); dec.Decision != policy.Allow || out.IntentStatus != intent.StateSucceeded {
		t.Fatalf("first purchase should fit: %+v", dec)
	}
	if dec, _ := h.buy(t, issued.AgentID, "groceries"); dec.Decision != policy.Deny || !slices.Contains(dec.ReasonCodes, spendpass.ReasonOverBudget) {
		t.Fatalf("second purchase must exceed the budget: %+v", dec)
	}
}

// fakeEconSpend is what economic intents have used, by pass.
type fakeEconSpend map[string]int64

func (f fakeEconSpend) PassSpend(_ context.Context, passID string, _ time.Time) (int64, error) {
	return f[passID], nil
}

// A pass has one budget: what its agent spent through economic intents shows
// in the pass's view and counts against what the shopping flow may still buy.
func TestSpendPass_EconomicSpendCountsAgainstTheOneBudget(t *testing.T) {
	h := newPassHarness(t)
	ctx := context.Background()
	issued, err := h.passes.Create(ctx, "user-1", spendpass.Pass{
		Label: "Both kinds", BudgetMinorUnits: 10000, AllowedCategories: []string{"groceries"}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	viaIntents := fakeEconSpend{issued.ID: 3000}
	h.passes.CountEconomicSpend(viaIntents)

	view, err := h.passes.ForAgent(ctx, issued.AgentID)
	if err != nil || view.SpentMinorUnits != 3000 || view.RemainingMinor != 7000 {
		t.Fatalf("the view must count intent spend: %+v %v", view, err)
	}
	// ₹85 fits a ₹100 budget on its own (see TestSpendPass_RefusesWhatThePassDoesntAllow)
	// but not with ₹30 already used through economic intents.
	if dec, _ := h.buy(t, issued.AgentID, "groceries"); dec.Decision != policy.Deny || !slices.Contains(dec.ReasonCodes, spendpass.ReasonOverBudget) {
		t.Fatalf("a purchase over what intents left must be denied: %+v", dec)
	}
}

func TestSpendPass_AskMeLineTightensGuardrails(t *testing.T) {
	h := newPassHarness(t)
	issued, err := h.passes.Create(context.Background(), "user-1", spendpass.Pass{
		Label: "Ask me always", BudgetMinorUnits: 100000, ApproveAboveMinorUnits: i64p(0), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	// ₹85 is under the platform's ₹1,000 approval line, but the pass says ask.
	dec, _ := h.buy(t, issued.AgentID, "groceries")
	if dec.Decision != policy.RequireApproval || !slices.Contains(dec.ReasonCodes, spendpass.ReasonApprovalRequired) {
		t.Fatalf("the pass's ask-me line must require approval: %+v", dec)
	}
}

func TestSpendPass_RevokeStopsTheAgent(t *testing.T) {
	h := newPassHarness(t)
	ctx := context.Background()
	issued, err := h.passes.Create(ctx, "user-1", spendpass.Pass{Label: "Short-lived", BudgetMinorUnits: 100000, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, out := h.buy(t, issued.AgentID, "groceries")
	if out == nil || out.Receipt == "" {
		t.Fatal("expected a first purchase with a receipt")
	}
	if err := h.passes.Revoke(ctx, "someone-else", issued.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("only the owner may revoke a pass, got %v", err)
	}
	if err := h.passes.Revoke(ctx, "user-1", issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "user-1", AgentID: issued.AgentID, Items: []intent.Item{{Query: "chips", Quantity: 1}},
		Constraints: intent.Constraints{MaxTotalMinorUnits: 40000, Currency: "INR"},
	}); err == nil {
		t.Fatal("a revoked pass's agent must not be able to start a purchase")
	}
	if v := h.receipts.Verify(ctx, out.Receipt); !v.Valid || v.Pass == nil || !v.Pass.Revoked {
		t.Fatalf("old receipts stay valid and show the pass as revoked: %+v", v.Pass)
	}
}
