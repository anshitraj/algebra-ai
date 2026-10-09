// Package spendpass defines the Spend Pass: a person's limited, revocable
// permission for one AI agent to spend on their behalf. It's what a person
// hands Claude, ChatGPT or a company's shopping bot instead of a card: a
// budget, the kinds of things it may buy, where, and above what amount it
// must ask first — until it expires or is revoked.
//
// A pass never loosens anything. Every purchase must clear the person's own
// guardrails AND the pass; the stricter answer wins.
package spendpass

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/policy"
)

// Period is how the budget renews.
type Period string

const (
	// PeriodTotal: one budget for the pass's whole life.
	PeriodTotal Period = "total"
	// PeriodWeek / PeriodMonth: a rolling 7- or 30-day window.
	PeriodWeek  Period = "week"
	PeriodMonth Period = "month"
)

// AgentKind says what the pass is for, for display and connection help.
type AgentKind string

const (
	AgentClaude  AgentKind = "claude"
	AgentChatGPT AgentKind = "chatgpt"
	AgentCustom  AgentKind = "custom"
)

// MaxLifetime bounds how long a pass may run: long-lived spending
// permissions are how money leaks.
const MaxLifetime = 366 * 24 * time.Hour

// Reason codes a pass adds to a policy decision.
const (
	ReasonOK                 = "PASS_OK"
	ReasonRevoked            = "PASS_REVOKED"
	ReasonExpired            = "PASS_EXPIRED"
	ReasonCategoryNotAllowed = "PASS_CATEGORY_NOT_ALLOWED"
	ReasonMerchantNotAllowed = "PASS_MERCHANT_NOT_ALLOWED"
	ReasonOverPerPurchase    = "PASS_PER_PURCHASE_LIMIT"
	ReasonOverBudget         = "PASS_BUDGET_EXCEEDED"
	ReasonApprovalRequired   = "PASS_APPROVAL_REQUIRED"
	ReasonCurrencyMismatch   = "PASS_CURRENCY_MISMATCH"
)

type Pass struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	AgentID   string    `json:"agent_id"`
	Label     string    `json:"label"`
	AgentKind AgentKind `json:"agent_kind"`
	Currency  string    `json:"currency"`

	BudgetMinorUnits int64  `json:"budget_minor_units"`
	BudgetPeriod     Period `json:"budget_period"`
	// MaxPerPurchaseMinorUnits caps one purchase; nil means only the budget
	// (and the person's guardrails) limit it.
	MaxPerPurchaseMinorUnits *int64 `json:"max_per_purchase_minor_units,omitempty"`
	// ApproveAboveMinorUnits: purchases at or above this wait for the
	// person, whatever their guardrails say. Nil adds no rule; 0 means ask
	// every time.
	ApproveAboveMinorUnits *int64 `json:"approve_above_minor_units,omitempty"`
	// AllowedCategories / AllowedMerchants restrict what and where; empty
	// means no restriction beyond the person's guardrails.
	AllowedCategories []string `json:"allowed_categories"`
	AllowedMerchants  []string `json:"allowed_merchants"`
	// Controls are the limits an autonomous agent needs that a shopping
	// budget doesn't: how fast it may spend and how it treats providers it
	// has never paid (see controls.go).
	Controls Controls `json:"controls"`
	// FrozenAt is set while the kill switch is on for this pass: nothing is
	// authorized, not even a payment already in flight. Unlike revoking, it
	// can be lifted.
	FrozenAt *time.Time `json:"frozen_at,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Normalize cleans a pass being created and reports what's wrong with it.
// knownCategories is the category vocabulary guardrails use.
func (p Pass) Normalize(now time.Time, knownCategories []string) (Pass, error) {
	p.Label = strings.TrimSpace(p.Label)
	if p.Label == "" || len(p.Label) > 80 {
		return p, errors.New("give the pass a name of up to 80 characters, e.g. \"Claude — groceries\"")
	}
	switch p.AgentKind {
	case AgentClaude, AgentChatGPT, AgentCustom:
	case "":
		p.AgentKind = AgentCustom
	default:
		return p, fmt.Errorf("unknown agent kind %q", p.AgentKind)
	}
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	if p.Currency == "" {
		p.Currency = "INR"
	}
	// INR for shopping; USDC (micro-units) for paid APIs and data.
	if p.Currency != "INR" && p.Currency != "USDC" {
		return p, fmt.Errorf("currency must be INR or USDC, not %q", p.Currency)
	}
	if p.BudgetMinorUnits <= 0 {
		return p, errors.New("the budget must be more than zero")
	}
	switch p.BudgetPeriod {
	case PeriodTotal, PeriodWeek, PeriodMonth:
	case "":
		p.BudgetPeriod = PeriodTotal
	default:
		return p, fmt.Errorf("budget period must be total, week or month, not %q", p.BudgetPeriod)
	}
	if m := p.MaxPerPurchaseMinorUnits; m != nil && (*m <= 0 || *m > p.BudgetMinorUnits) {
		return p, errors.New("the per-purchase limit must be more than zero and no more than the budget")
	}
	if a := p.ApproveAboveMinorUnits; a != nil && *a < 0 {
		return p, errors.New("the ask-me-above amount can't be negative")
	}
	cats := []string{}
	for _, c := range p.AllowedCategories {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || slices.Contains(cats, c) {
			continue
		}
		if !slices.Contains(knownCategories, c) {
			return p, fmt.Errorf("unknown category %q", c)
		}
		cats = append(cats, c)
	}
	p.AllowedCategories = cats
	merchants := []string{}
	for _, m := range p.AllowedMerchants {
		m = strings.ToLower(strings.TrimSpace(m))
		if m != "" && len(m) <= 64 && !slices.Contains(merchants, m) {
			merchants = append(merchants, m)
		}
	}
	p.AllowedMerchants = merchants
	var err error
	if p.Controls, err = p.Controls.Normalize(p.Currency); err != nil {
		return p, err
	}
	if !p.ExpiresAt.After(now) {
		return p, errors.New("the pass must expire in the future")
	}
	if p.ExpiresAt.Sub(now) > MaxLifetime {
		return p, errors.New("a pass can last at most a year")
	}
	return p, nil
}

// Active reports whether the pass can authorize anything at now.
func (p Pass) Active(now time.Time) bool {
	return p.RevokedAt == nil && now.Before(p.ExpiresAt)
}

// WindowStart is where the current budget window begins: the pass's
// creation for a one-off budget, else a rolling 7 or 30 days.
func (p Pass) WindowStart(now time.Time) time.Time {
	switch p.BudgetPeriod {
	case PeriodWeek:
		return later(now.Add(-7*24*time.Hour), p.CreatedAt)
	case PeriodMonth:
		return later(now.Add(-30*24*time.Hour), p.CreatedAt)
	default:
		return p.CreatedAt
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Remaining is what's left of the budget in the current window.
func (p Pass) Remaining(spentInWindow int64) int64 {
	return max(p.BudgetMinorUnits-spentInWindow, 0)
}

// Purchase is what a pass is asked to authorize.
type Purchase struct {
	Merchant      string
	Category      string
	AmountMinor   int64
	Currency      string
	SpentInWindow int64
}

// Evaluate is the pass's own verdict on a purchase: DENY for anything
// outside its scope, REQUIRE_APPROVAL at or above its ask-me line, else
// ALLOW. It is combined with the person's guardrails by Combine.
func (p Pass) Evaluate(now time.Time, in Purchase) *policy.PolicyDecision {
	deny := func(code string) *policy.PolicyDecision { return decision(policy.Deny, code, now) }
	switch {
	case p.RevokedAt != nil:
		return deny(ReasonRevoked)
	case p.FrozenAt != nil:
		return deny(ReasonFrozen)
	case !now.Before(p.ExpiresAt):
		return deny(ReasonExpired)
	case in.Currency != "" && !strings.EqualFold(in.Currency, p.Currency):
		return deny(ReasonCurrencyMismatch)
	case len(p.AllowedCategories) > 0 && !slices.Contains(p.AllowedCategories, strings.ToLower(in.Category)):
		return deny(ReasonCategoryNotAllowed)
	case len(p.AllowedMerchants) > 0 && !slices.Contains(p.AllowedMerchants, strings.ToLower(in.Merchant)):
		return deny(ReasonMerchantNotAllowed)
	case p.MaxPerPurchaseMinorUnits != nil && in.AmountMinor > *p.MaxPerPurchaseMinorUnits:
		return deny(ReasonOverPerPurchase)
	case in.SpentInWindow+in.AmountMinor > p.BudgetMinorUnits:
		return deny(ReasonOverBudget)
	case p.ApproveAboveMinorUnits != nil && in.AmountMinor >= *p.ApproveAboveMinorUnits:
		return decision(policy.RequireApproval, ReasonApprovalRequired, now)
	}
	return decision(policy.Allow, ReasonOK, now)
}

func decision(d policy.Decision, code string, now time.Time) *policy.PolicyDecision {
	return &policy.PolicyDecision{Decision: d, ReasonCodes: []string{code}, PolicyVersion: "spend-pass", EvaluatedAt: now}
}

// Combine merges the person's guardrail decision with the pass's: the
// stricter verdict wins (DENY > REQUIRE_APPROVAL > ALLOW) and every reason
// is kept, so the person can see which rule decided.
func Combine(guardrails, pass *policy.PolicyDecision, passID string) *policy.PolicyDecision {
	out := *guardrails
	out.ReasonCodes = append(slices.Clone(guardrails.ReasonCodes), pass.ReasonCodes...)
	out.PolicyVersion = guardrails.PolicyVersion + "+pass:" + passID
	if rank(pass.Decision) > rank(guardrails.Decision) {
		out.Decision = pass.Decision
		if pass.Decision == policy.RequireApproval && out.ApprovalRequirement == nil {
			out.ApprovalRequirement = pass.ApprovalRequirement
		}
	}
	return &out
}

func rank(d policy.Decision) int {
	switch d {
	case policy.Deny:
		return 2
	case policy.RequireApproval:
		return 1
	default:
		return 0
	}
}
