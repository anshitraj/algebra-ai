// Package billing models what a user pays Algebra itself for — the plans on
// the pricing page — and what each plan entitles them to. It is entirely
// separate from purchase payments: Algebra is non-custodial for what users
// buy, and this package never touches that money.
package billing

import (
	"context"
	"errors"
	"time"
)

// Plan is a pricing tier.
type Plan string

const (
	PlanDeveloper Plan = "developer" // free
	PlanGrowth    Plan = "growth"    // paid subscription
)

// Entitlement is what a plan allows per calendar month.
type Entitlement struct {
	// IncludedExecutions is how many orders a month the plan covers.
	IncludedExecutions int `json:"included_executions"`
	// HardLimit: executions beyond IncludedExecutions are refused (free
	// tier). When false they're allowed and counted as overage.
	HardLimit bool `json:"hard_limit"`
}

// Entitlements mirror the pricing page (frontend/components/pricing-tiers.tsx).
var Entitlements = map[Plan]Entitlement{
	PlanDeveloper: {IncludedExecutions: 100, HardLimit: true},
	PlanGrowth:    {IncludedExecutions: 5000, HardLimit: false},
}

// Status is the Razorpay subscription lifecycle, verbatim, so webhook
// payloads map straight onto it.
type Status string

const (
	StatusCreated       Status = "created"       // subscription created, checkout not completed
	StatusAuthenticated Status = "authenticated" // mandate/payment authorised, first charge pending
	StatusActive        Status = "active"
	StatusPending       Status = "pending" // a renewal charge failed; Razorpay is retrying
	StatusHalted        Status = "halted"  // retries exhausted
	StatusCancelled     Status = "cancelled"
	StatusCompleted     Status = "completed"
	StatusExpired       Status = "expired"
)

// Grants reports whether a subscription in this status gives paid-plan
// access. pending keeps access while Razorpay retries a failed renewal —
// cutting a user off on the first soft decline would be hostile.
func (s Status) Grants() bool {
	switch s {
	case StatusAuthenticated, StatusActive, StatusPending:
		return true
	}
	return false
}

// Terminal reports whether the subscription can never become active again.
func (s Status) Terminal() bool {
	switch s {
	case StatusCancelled, StatusCompleted, StatusExpired, StatusHalted:
		return true
	}
	return false
}

// Subscription is a user's paid plan. One row per user: upgrading again
// after a cancellation replaces it.
type Subscription struct {
	UserID                 string     `json:"-"`
	Plan                   Plan       `json:"plan"`
	Provider               string     `json:"provider"`
	ProviderSubscriptionID string     `json:"provider_subscription_id"`
	Status                 Status     `json:"status"`
	CurrentPeriodStart     *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd       *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd      bool       `json:"cancel_at_period_end"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// EffectivePlan is the plan a user is on right now.
func EffectivePlan(sub *Subscription) Plan {
	if sub != nil && sub.Status.Grants() {
		return sub.Plan
	}
	return PlanDeveloper
}

// GatewaySubscription is a payment gateway's view of a subscription.
type GatewaySubscription struct {
	ID           string
	PlanID       string
	Status       Status
	CurrentStart *time.Time
	CurrentEnd   *time.Time
	Notes        map[string]string
}

// Gateway is the payment provider Algebra bills its plans through
// (providers/razorpay). Signature checks live here so no caller can forget
// them.
type Gateway interface {
	KeyID() string
	TestMode() bool
	CreateMonthlyPlan(ctx context.Context, name string, amountMinor int64, currency, description string) (planID string, err error)
	CreateSubscription(ctx context.Context, planID string, totalCount int, notes map[string]string) (*GatewaySubscription, error)
	GetSubscription(ctx context.Context, id string) (*GatewaySubscription, error)
	CancelSubscription(ctx context.Context, id string, atCycleEnd bool) (*GatewaySubscription, error)
	VerifySubscriptionPayment(paymentID, subscriptionID, signature string) error
	VerifyWebhook(body []byte, signature string) error
}

// ErrQuotaExceeded is returned when the free tier's monthly executions are
// used up. The transport maps it to 402 Payment Required.
var ErrQuotaExceeded = errors.New("this month's included order executions are used up — upgrade to Growth to keep ordering")
