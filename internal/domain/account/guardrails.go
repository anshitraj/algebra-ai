package account

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/project-algebra/algebra/policy"
)

// Guardrails is the human-sized view of a user's spending policy — the four
// or five knobs a person actually reasons about ("ask me above ₹1,000",
// "never more than ₹5,000 a day", "never buy gift cards"). ToRules turns it
// into the real policy.Rules the deterministic engine evaluates; the agent
// and the LLM never see or touch this directly.
type Guardrails struct {
	Currency string `json:"currency"`

	// ApprovalThresholdMinorUnits: any purchase at or above this needs the
	// user's explicit approval. 0 means "always ask me".
	ApprovalThresholdMinorUnits int64 `json:"approval_threshold_minor_units"`

	// MaxPerPurchaseMinorUnits/MaxPerDayMinorUnits are hard caps — denied
	// outright, no approval can override them.
	MaxPerPurchaseMinorUnits int64 `json:"max_per_purchase_minor_units"`
	MaxPerDayMinorUnits      int64 `json:"max_per_day_minor_units"`

	BlockedCategories []string `json:"blocked_categories"`
	BlockedMerchants  []string `json:"blocked_merchants,omitempty"`

	InternationalRequiresApproval bool `json:"international_requires_approval"`
}

// PlatformMaxPerDayMinorUnits is the ceiling no user guardrail may exceed
// (₹5,00,000). A consumer control plane with no KYC tier has no business
// authorizing more than this per day on a single account.
const PlatformMaxPerDayMinorUnits int64 = 50_000_000

// KnownCategories are the category hints the agent tags intents with and
// that the guardrail UI offers to block. Anything else is rejected so a
// typo can't silently create a rule that never matches.
var KnownCategories = []string{
	"groceries", "food_delivery", "electronics", "fashion", "home",
	"beauty", "pharmacy", "gift_cards", "alcohol", "tobacco", "subscriptions", "travel",
	// digital_services: paid APIs and data an agent buys per call (x402).
	"digital_services",
}

// DefaultGuardrails mirrors policy.DefaultRules() so a user who skips
// onboarding gets exactly the platform default, not something looser.
func DefaultGuardrails() Guardrails {
	d := policy.DefaultRules()
	return Guardrails{
		Currency:                      d.Currency,
		ApprovalThresholdMinorUnits:   d.ApprovalThresholdMinorUnits,
		MaxPerPurchaseMinorUnits:      d.MaxPerTransactionMinorUnits,
		MaxPerDayMinorUnits:           d.MaxPerDayMinorUnits,
		BlockedCategories:             slices.Clone(d.BlockedCategories),
		InternationalRequiresApproval: d.InternationalRequiresApproval,
	}
}

// Normalize fills defaults and validates. It never loosens anything it
// doesn't understand — unknown categories are an error, not dropped.
func (g Guardrails) Normalize() (Guardrails, error) {
	if g.Currency == "" {
		g.Currency = "INR"
	}
	g.Currency = strings.ToUpper(g.Currency)
	if g.MaxPerDayMinorUnits <= 0 {
		return g, errors.New("daily limit must be greater than zero")
	}
	if g.MaxPerDayMinorUnits > PlatformMaxPerDayMinorUnits {
		return g, fmt.Errorf("daily limit can't exceed %d minor units", PlatformMaxPerDayMinorUnits)
	}
	if g.MaxPerPurchaseMinorUnits <= 0 || g.MaxPerPurchaseMinorUnits > g.MaxPerDayMinorUnits {
		g.MaxPerPurchaseMinorUnits = g.MaxPerDayMinorUnits
	}
	if g.ApprovalThresholdMinorUnits < 0 {
		return g, errors.New("approval threshold can't be negative")
	}
	cats := make([]string, 0, len(g.BlockedCategories))
	for _, c := range g.BlockedCategories {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || slices.Contains(cats, c) {
			continue
		}
		if !slices.Contains(KnownCategories, c) {
			return g, fmt.Errorf("unknown category %q", c)
		}
		cats = append(cats, c)
	}
	g.BlockedCategories = cats
	merchants := make([]string, 0, len(g.BlockedMerchants))
	for _, m := range g.BlockedMerchants {
		m = strings.TrimSpace(m)
		if m != "" && !slices.Contains(merchants, m) {
			merchants = append(merchants, m)
		}
	}
	g.BlockedMerchants = merchants
	return g, nil
}

// ToRules builds the policy.Rules the engine evaluates. Everything the
// guardrails don't cover (payment/shipping alias allow-lists, crypto-rail
// rules) is inherited from policy.DefaultRules unchanged.
func (g Guardrails) ToRules() policy.Rules {
	r := policy.DefaultRules()
	r.Version = "user-guardrails-v1"
	r.Currency = g.Currency
	r.MaxPerTransactionMinorUnits = g.MaxPerPurchaseMinorUnits
	r.MaxPerDayMinorUnits = g.MaxPerDayMinorUnits
	// The engine treats a zero threshold as "no threshold", so "always ask
	// me" is expressed as 1 minor unit — every real purchase is ≥ that.
	r.ApprovalThresholdMinorUnits = g.ApprovalThresholdMinorUnits
	if r.ApprovalThresholdMinorUnits == 0 {
		r.ApprovalThresholdMinorUnits = 1
	}
	r.BlockedCategories = slices.Clone(g.BlockedCategories)
	r.BlockedMerchants = slices.Clone(g.BlockedMerchants)
	r.InternationalRequiresApproval = g.InternationalRequiresApproval
	return r
}
