package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/billing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type fakeBillingStore struct {
	mu     sync.Mutex
	subs   map[string]*billing.Subscription
	plans  map[string]string
	events map[string]bool
	used   int
}

func newFakeBillingStore() *fakeBillingStore {
	return &fakeBillingStore{subs: map[string]*billing.Subscription{}, plans: map[string]string{}, events: map[string]bool{}}
}

func (f *fakeBillingStore) GetSubscription(_ context.Context, userID string) (*billing.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.subs[userID]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeBillingStore) GetSubscriptionByProviderID(_ context.Context, id string) (*billing.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		if s.ProviderSubscriptionID == id {
			cp := *s
			return &cp, nil
		}
	}
	return nil, shared.ErrNotFound
}

func (f *fakeBillingStore) UpsertSubscription(_ context.Context, s *billing.Subscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *s
	f.subs[s.UserID] = &cp
	return nil
}

func (f *fakeBillingStore) GetProviderPlanID(_ context.Context, plan billing.Plan, _ int64, _ string) (string, error) {
	if id, ok := f.plans[string(plan)]; ok {
		return id, nil
	}
	return "", shared.ErrNotFound
}

func (f *fakeBillingStore) SaveProviderPlanID(_ context.Context, plan billing.Plan, id string, _ int64, _ string) error {
	f.plans[string(plan)] = id
	return nil
}

func (f *fakeBillingStore) RecordEvent(_ context.Context, id, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events[id] {
		return false, nil
	}
	f.events[id] = true
	return true, nil
}

func (f *fakeBillingStore) CountExecutionsSince(context.Context, string, time.Time) (int, error) {
	return f.used, nil
}

type fakeGateway struct {
	plansCreated int
	subs         map[string]*billing.GatewaySubscription
}

func (g *fakeGateway) KeyID() string  { return "rzp_test_key" }
func (g *fakeGateway) TestMode() bool { return true }
func (g *fakeGateway) CreateMonthlyPlan(context.Context, string, int64, string, string) (string, error) {
	g.plansCreated++
	return "plan_growth", nil
}
func (g *fakeGateway) CreateSubscription(_ context.Context, planID string, _ int, notes map[string]string) (*billing.GatewaySubscription, error) {
	s := &billing.GatewaySubscription{ID: "sub_" + notes["user_id"], PlanID: planID, Status: billing.StatusCreated}
	g.subs[s.ID] = s
	return s, nil
}
func (g *fakeGateway) GetSubscription(_ context.Context, id string) (*billing.GatewaySubscription, error) {
	s, ok := g.subs[id]
	if !ok {
		return nil, errors.New("no such subscription")
	}
	return s, nil
}
func (g *fakeGateway) CancelSubscription(_ context.Context, id string, atEnd bool) (*billing.GatewaySubscription, error) {
	s := g.subs[id]
	if !atEnd {
		s.Status = billing.StatusCancelled
	}
	return s, nil
}
func (g *fakeGateway) VerifySubscriptionPayment(paymentID, subscriptionID, signature string) error {
	if signature != "sig:"+paymentID+"|"+subscriptionID {
		return errors.New("bad signature")
	}
	return nil
}
func (g *fakeGateway) VerifyWebhook(body []byte, signature string) error {
	if signature != "ok" {
		return errors.New("bad webhook signature")
	}
	return nil
}

func newBilling() (*BillingService, *fakeBillingStore, *fakeGateway) {
	store := newFakeBillingStore()
	gw := &fakeGateway{subs: map[string]*billing.GatewaySubscription{}}
	return NewBillingService(store, gw, BillingConfig{GrowthAmountMinor: 849900}), store, gw
}

func TestBilling_CheckoutConfirmAndPlanAccess(t *testing.T) {
	svc, store, gw := newBilling()
	ctx := context.Background()

	co, err := svc.StartCheckout(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if co.KeyID != "rzp_test_key" || co.SubscriptionID != "sub_u1" || gw.plansCreated != 1 {
		t.Fatalf("unexpected checkout: %+v (plans created %d)", co, gw.plansCreated)
	}
	// Reopening checkout reuses the unfinished subscription and plan.
	if again, _ := svc.StartCheckout(ctx, "u1"); again.SubscriptionID != co.SubscriptionID || gw.plansCreated != 1 {
		t.Error("abandoned checkout should be reused, not duplicated")
	}
	if st, _ := svc.Status(ctx, "u1"); st.Plan != billing.PlanDeveloper {
		t.Errorf("unpaid checkout must not grant Growth, got %s", st.Plan)
	}

	// A forged confirmation — or one for someone else's subscription — fails.
	if _, err := svc.ConfirmCheckout(ctx, "u1", "pay_1", "sub_u1", "forged"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Errorf("forged signature: got %v", err)
	}
	if _, err := svc.ConfirmCheckout(ctx, "u2", "pay_1", "sub_u1", "sig:pay_1|sub_u1"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another user's subscription: got %v", err)
	}

	sub, err := svc.ConfirmCheckout(ctx, "u1", "pay_1", "sub_u1", "sig:pay_1|sub_u1")
	if err != nil || sub.Status != billing.StatusAuthenticated {
		t.Fatalf("confirm: %v %+v", err, sub)
	}
	if st, _ := svc.Status(ctx, "u1"); st.Plan != billing.PlanGrowth {
		t.Errorf("confirmed subscription should grant Growth, got %s", st.Plan)
	}
	if _, err := svc.StartCheckout(ctx, "u1"); !errors.Is(err, shared.ErrConflict) {
		t.Errorf("second checkout while on Growth: got %v", err)
	}
	_ = store
}

func TestBilling_FreeTierIsCappedGrowthIsNot(t *testing.T) {
	svc, store, _ := newBilling()
	ctx := context.Background()
	store.used = 99
	if err := svc.CheckExecution(ctx, "u1"); err != nil {
		t.Errorf("99 of 100 used should allow one more: %v", err)
	}
	store.used = 100
	if err := svc.CheckExecution(ctx, "u1"); !errors.Is(err, billing.ErrQuotaExceeded) {
		t.Errorf("100 of 100 used: got %v", err)
	}
	_ = store.UpsertSubscription(ctx, &billing.Subscription{UserID: "u1", Plan: billing.PlanGrowth, ProviderSubscriptionID: "sub_x", Status: billing.StatusActive})
	store.used = 9000
	if err := svc.CheckExecution(ctx, "u1"); err != nil {
		t.Errorf("Growth counts overage but never blocks: %v", err)
	}
}

func TestBilling_WebhookVerifiedAndAppliedOnce(t *testing.T) {
	svc, store, _ := newBilling()
	ctx := context.Background()
	_ = store.UpsertSubscription(ctx, &billing.Subscription{UserID: "u1", Plan: billing.PlanGrowth, ProviderSubscriptionID: "sub_u1", Status: billing.StatusActive})

	halted := []byte(`{"event":"subscription.halted","payload":{"subscription":{"entity":{"id":"sub_u1","status":"halted","current_end":1893456000}}}}`)
	if err := svc.HandleWebhook(ctx, halted, "forged", "evt_1"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Fatalf("forged webhook: got %v", err)
	}
	if s, _ := store.GetSubscription(ctx, "u1"); s.Status != billing.StatusActive {
		t.Fatal("forged webhook changed state")
	}
	if err := svc.HandleWebhook(ctx, halted, "ok", "evt_1"); err != nil {
		t.Fatal(err)
	}
	if s, _ := store.GetSubscription(ctx, "u1"); s.Status != billing.StatusHalted || s.CurrentPeriodEnd == nil {
		t.Fatalf("halted webhook not applied: %+v", s)
	}

	// A replayed older event (same ID) must not resurrect the subscription.
	_ = store.UpsertSubscription(ctx, &billing.Subscription{UserID: "u1", Plan: billing.PlanGrowth, ProviderSubscriptionID: "sub_u1", Status: billing.StatusCancelled})
	if err := svc.HandleWebhook(ctx, halted, "ok", "evt_1"); err != nil {
		t.Fatal(err)
	}
	if s, _ := store.GetSubscription(ctx, "u1"); s.Status != billing.StatusCancelled {
		t.Errorf("replayed event was applied twice: %s", s.Status)
	}
}

func TestBilling_CancelKeepsAccessUntilPeriodEnd(t *testing.T) {
	svc, store, gw := newBilling()
	ctx := context.Background()
	gw.subs["sub_u1"] = &billing.GatewaySubscription{ID: "sub_u1", Status: billing.StatusActive}
	_ = store.UpsertSubscription(ctx, &billing.Subscription{UserID: "u1", Plan: billing.PlanGrowth, ProviderSubscriptionID: "sub_u1", Status: billing.StatusActive})

	sub, err := svc.Cancel(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !sub.CancelAtPeriodEnd || !sub.Status.Grants() {
		t.Errorf("cancel should keep Growth until the period ends: %+v", sub)
	}
}

func TestBilling_NotConfigured(t *testing.T) {
	svc := NewBillingService(newFakeBillingStore(), nil, BillingConfig{})
	if _, err := svc.StartCheckout(context.Background(), "u1"); !errors.Is(err, ErrBillingNotConfigured) {
		t.Errorf("got %v", err)
	}
	st, err := svc.Status(context.Background(), "u1")
	if err != nil || st.CheckoutAvailable || st.Plan != billing.PlanDeveloper {
		t.Errorf("unconfigured status: %+v %v", st, err)
	}
}
