package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/billing"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// BillingStore persists subscriptions, the gateway plan IDs Algebra created,
// processed webhook event IDs, and answers usage questions.
type BillingStore interface {
	GetSubscription(ctx context.Context, userID string) (*billing.Subscription, error) // shared.ErrNotFound
	GetSubscriptionByProviderID(ctx context.Context, providerSubscriptionID string) (*billing.Subscription, error)
	UpsertSubscription(ctx context.Context, s *billing.Subscription) error
	GetProviderPlanID(ctx context.Context, plan billing.Plan, amountMinor int64, currency string) (string, error) // shared.ErrNotFound
	SaveProviderPlanID(ctx context.Context, plan billing.Plan, providerPlanID string, amountMinor int64, currency string) error
	// RecordEvent stores a webhook event ID and reports whether it was new —
	// the replay guard.
	RecordEvent(ctx context.Context, eventID, event string) (bool, error)
	CountExecutionsSince(ctx context.Context, userID string, since time.Time) (int, error)
}

// BillingConfig is the price of the Growth plan and, optionally, a plan the
// operator already created in the Razorpay dashboard.
type BillingConfig struct {
	GrowthPlanID      string // optional: use this plan as-is
	GrowthAmountMinor int64  // used to create the plan when GrowthPlanID is empty
	Currency          string
}

// ErrBillingNotConfigured means no payment gateway keys are set.
var ErrBillingNotConfigured = fmt.Errorf("%w: billing is not configured (set RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET)", shared.ErrNotImplemented)

// BillingService sells Algebra's own plans through a payment gateway and
// enforces what each plan includes. It never touches the money for what
// users buy.
type BillingService struct {
	store   BillingStore
	gateway billing.Gateway // nil when not configured
	cfg     BillingConfig
	now     func() time.Time
}

func NewBillingService(store BillingStore, gateway billing.Gateway, cfg BillingConfig) *BillingService {
	if cfg.Currency == "" {
		cfg.Currency = "INR"
	}
	return &BillingService{store: store, gateway: gateway, cfg: cfg, now: time.Now}
}

// monthStart is the first instant of the current calendar month (UTC) —
// the usage window for every plan.
func monthStart(now time.Time) time.Time {
	n := now.UTC()
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// BillingStatus is everything the billing page shows.
type BillingStatus struct {
	Plan              billing.Plan          `json:"plan"`
	Entitlement       billing.Entitlement   `json:"entitlement"`
	UsedThisMonth     int                   `json:"used_this_month"`
	PeriodStart       time.Time             `json:"period_start"`
	Subscription      *billing.Subscription `json:"subscription,omitempty"`
	CheckoutAvailable bool                  `json:"checkout_available"`
	TestMode          bool                  `json:"test_mode"`
	GrowthPrice       int64                 `json:"growth_price_minor_units"`
	Currency          string                `json:"currency"`
}

func (s *BillingService) subscription(ctx context.Context, userID string) (*billing.Subscription, error) {
	sub, err := s.store.GetSubscription(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, nil
	}
	return sub, err
}

// Status reports the user's plan and usage. A subscription still settling
// (just created or authenticated) is refreshed from the gateway so the page
// is right even where webhooks can't reach (local development).
func (s *BillingService) Status(ctx context.Context, userID string) (*BillingStatus, error) {
	sub, err := s.subscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sub != nil && s.gateway != nil && (sub.Status == billing.StatusCreated || sub.Status == billing.StatusAuthenticated || sub.Status == billing.StatusPending) {
		if gs, err := s.gateway.GetSubscription(ctx, sub.ProviderSubscriptionID); err == nil {
			sub = s.apply(sub, gs)
			_ = s.store.UpsertSubscription(ctx, sub)
		}
	}
	start := monthStart(s.now())
	used, err := s.store.CountExecutionsSince(ctx, userID, start)
	if err != nil {
		return nil, err
	}
	plan := billing.EffectivePlan(sub)
	st := &BillingStatus{
		Plan: plan, Entitlement: billing.Entitlements[plan], UsedThisMonth: used, PeriodStart: start,
		Subscription: sub, CheckoutAvailable: s.gateway != nil, GrowthPrice: s.cfg.GrowthAmountMinor, Currency: s.cfg.Currency,
	}
	if s.gateway != nil {
		st.TestMode = s.gateway.TestMode()
	}
	return st, nil
}

// Plans is the public price list — what the pricing page shows, from the
// same numbers checkout charges, so the two can never disagree.
type Plans struct {
	Currency          string `json:"currency"`
	GrowthPriceMinor  int64  `json:"growth_price_minor_units"`
	DeveloperIncluded int    `json:"developer_included_executions"`
	GrowthIncluded    int    `json:"growth_included_executions"`
	CheckoutAvailable bool   `json:"checkout_available"`
}

func (s *BillingService) Plans() Plans {
	return Plans{
		Currency: s.cfg.Currency, GrowthPriceMinor: s.cfg.GrowthAmountMinor,
		DeveloperIncluded: billing.Entitlements[billing.PlanDeveloper].IncludedExecutions,
		GrowthIncluded:    billing.Entitlements[billing.PlanGrowth].IncludedExecutions,
		CheckoutAvailable: s.gateway != nil,
	}
}

// Checkout is what the browser needs to open the gateway's checkout. KeyID
// is the public key; the secret never leaves the server.
type Checkout struct {
	KeyID          string `json:"key_id"`
	SubscriptionID string `json:"subscription_id"`
	TestMode       bool   `json:"test_mode"`
}

func (s *BillingService) growthPlanID(ctx context.Context) (string, error) {
	if s.cfg.GrowthPlanID != "" {
		return s.cfg.GrowthPlanID, nil
	}
	id, err := s.store.GetProviderPlanID(ctx, billing.PlanGrowth, s.cfg.GrowthAmountMinor, s.cfg.Currency)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, shared.ErrNotFound) {
		return "", err
	}
	if s.cfg.GrowthAmountMinor <= 0 {
		return "", errors.New("app: set RAZORPAY_PLAN_GROWTH or a positive GROWTH_PRICE_INR")
	}
	id, err = s.gateway.CreateMonthlyPlan(ctx, "Algebra Growth", s.cfg.GrowthAmountMinor, s.cfg.Currency,
		"5,000 order executions a month, real merchant connectors, email support")
	if err != nil {
		return "", fmt.Errorf("app: creating the Growth plan: %w", err)
	}
	if err := s.store.SaveProviderPlanID(ctx, billing.PlanGrowth, id, s.cfg.GrowthAmountMinor, s.cfg.Currency); err != nil {
		return "", err
	}
	return id, nil
}

// StartCheckout creates (or reuses an unfinished) Growth subscription for
// the user and returns what Checkout needs.
func (s *BillingService) StartCheckout(ctx context.Context, userID string) (*Checkout, error) {
	if s.gateway == nil {
		return nil, ErrBillingNotConfigured
	}
	sub, err := s.subscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sub != nil && sub.Status.Grants() {
		return nil, fmt.Errorf("%w: you're already on Growth", shared.ErrConflict)
	}
	// Reuse a checkout the user started but didn't finish, rather than
	// leaving a trail of abandoned subscriptions at the gateway.
	if sub != nil && sub.Status == billing.StatusCreated && s.now().Sub(sub.CreatedAt) < 24*time.Hour {
		return &Checkout{KeyID: s.gateway.KeyID(), SubscriptionID: sub.ProviderSubscriptionID, TestMode: s.gateway.TestMode()}, nil
	}
	planID, err := s.growthPlanID(ctx)
	if err != nil {
		return nil, err
	}
	// 60 monthly cycles (5 years) — Razorpay requires a finite count; users
	// cancel whenever they like.
	gs, err := s.gateway.CreateSubscription(ctx, planID, 60, map[string]string{"user_id": userID, "plan": string(billing.PlanGrowth)})
	if err != nil {
		return nil, fmt.Errorf("app: starting checkout: %w", err)
	}
	now := s.now()
	next := &billing.Subscription{
		UserID: userID, Plan: billing.PlanGrowth, Provider: "razorpay", ProviderSubscriptionID: gs.ID,
		Status: billing.StatusCreated, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertSubscription(ctx, next); err != nil {
		return nil, err
	}
	return &Checkout{KeyID: s.gateway.KeyID(), SubscriptionID: gs.ID, TestMode: s.gateway.TestMode()}, nil
}

// ConfirmCheckout verifies the signature Checkout returned and records the
// gateway's own view of the subscription. The browser's word alone never
// upgrades anyone.
func (s *BillingService) ConfirmCheckout(ctx context.Context, userID, paymentID, subscriptionID, signature string) (*billing.Subscription, error) {
	if s.gateway == nil {
		return nil, ErrBillingNotConfigured
	}
	if err := s.gateway.VerifySubscriptionPayment(paymentID, subscriptionID, signature); err != nil {
		return nil, fmt.Errorf("%w: %v", shared.ErrUnauthorized, err)
	}
	sub, err := s.subscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sub == nil || sub.ProviderSubscriptionID != subscriptionID {
		return nil, fmt.Errorf("%w: subscription %s", shared.ErrNotFound, subscriptionID)
	}
	gs, err := s.gateway.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("app: confirming subscription: %w", err)
	}
	sub = s.apply(sub, gs)
	// A verified signature proves the authorisation succeeded even if the
	// gateway hasn't moved the status yet.
	if sub.Status == billing.StatusCreated {
		sub.Status = billing.StatusAuthenticated
	}
	if err := s.store.UpsertSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// Cancel stops renewal at the end of the paid period; access continues
// until then.
func (s *BillingService) Cancel(ctx context.Context, userID string) (*billing.Subscription, error) {
	if s.gateway == nil {
		return nil, ErrBillingNotConfigured
	}
	sub, err := s.subscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sub == nil || sub.Status.Terminal() {
		return nil, fmt.Errorf("%w: no active subscription", shared.ErrNotFound)
	}
	// A subscription that was never authorised can't be cancelled "at cycle
	// end" — there is no cycle; cancel it outright.
	atEnd := sub.Status == billing.StatusActive || sub.Status == billing.StatusPending
	gs, err := s.gateway.CancelSubscription(ctx, sub.ProviderSubscriptionID, atEnd)
	if err != nil {
		return nil, fmt.Errorf("app: cancelling subscription: %w", err)
	}
	sub = s.apply(sub, gs)
	sub.CancelAtPeriodEnd = atEnd
	if err := s.store.UpsertSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *BillingService) apply(sub *billing.Subscription, gs *billing.GatewaySubscription) *billing.Subscription {
	cp := *sub
	// Never regress a verified authorisation back to "created": the gateway
	// can lag a few seconds behind the Checkout signature we already checked.
	if gs.Status != "" && !(gs.Status == billing.StatusCreated && sub.Status != billing.StatusCreated) {
		cp.Status = gs.Status
	}
	if gs.CurrentStart != nil {
		cp.CurrentPeriodStart = gs.CurrentStart
	}
	if gs.CurrentEnd != nil {
		cp.CurrentPeriodEnd = gs.CurrentEnd
	}
	if cp.Status.Terminal() {
		cp.CancelAtPeriodEnd = false
	}
	cp.UpdatedAt = s.now()
	return &cp
}

// HandleWebhook verifies and applies a gateway webhook: renewals, failed
// charges, cancellations. Each event ID is processed at most once.
func (s *BillingService) HandleWebhook(ctx context.Context, body []byte, signature, eventID string) error {
	if s.gateway == nil {
		return ErrBillingNotConfigured
	}
	if err := s.gateway.VerifyWebhook(body, signature); err != nil {
		return fmt.Errorf("%w: %v", shared.ErrUnauthorized, err)
	}
	var evt struct {
		Event   string `json:"event"`
		Payload struct {
			Subscription struct {
				Entity struct {
					ID           string `json:"id"`
					Status       string `json:"status"`
					CurrentStart int64  `json:"current_start"`
					CurrentEnd   int64  `json:"current_end"`
				} `json:"entity"`
			} `json:"subscription"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("app: decoding webhook: %w", err)
	}
	if eventID == "" {
		return errors.New("app: webhook has no event id")
	}
	fresh, err := s.store.RecordEvent(ctx, eventID, evt.Event)
	if err != nil {
		return err
	}
	if !fresh {
		return nil // replay or retry of an event already applied
	}
	ent := evt.Payload.Subscription.Entity
	if ent.ID == "" {
		return nil // not a subscription event (e.g. a one-off payment)
	}
	sub, err := s.store.GetSubscriptionByProviderID(ctx, ent.ID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil // not ours — e.g. created directly in the dashboard
		}
		return err
	}
	gs := &billing.GatewaySubscription{ID: ent.ID, Status: billing.Status(ent.Status)}
	if ent.CurrentStart > 0 {
		t := time.Unix(ent.CurrentStart, 0).UTC()
		gs.CurrentStart = &t
	}
	if ent.CurrentEnd > 0 {
		t := time.Unix(ent.CurrentEnd, 0).UTC()
		gs.CurrentEnd = &t
	}
	return s.store.UpsertSubscription(ctx, s.apply(sub, gs))
}

// CheckExecution is OrderService's gate: the free tier stops at its monthly
// allowance; paid plans count overage but never block an order.
func (s *BillingService) CheckExecution(ctx context.Context, userID string) error {
	sub, err := s.subscription(ctx, userID)
	if err != nil {
		return err
	}
	ent := billing.Entitlements[billing.EffectivePlan(sub)]
	if !ent.HardLimit {
		return nil
	}
	used, err := s.store.CountExecutionsSince(ctx, userID, monthStart(s.now()))
	if err != nil {
		return err
	}
	if used >= ent.IncludedExecutions {
		return billing.ErrQuotaExceeded
	}
	return nil
}
