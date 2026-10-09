// Package policy defines the PolicyProvider abstraction: the ONLY authority
// that may turn a purchase into ALLOW/DENY/REQUIRE_APPROVAL. Per the
// mandate: "Never let an LLM override a DENY result." Decisions are
// computed here from persisted state the caller provides — never from a
// client-supplied "decision" field.
package policy

import (
	"context"
	"time"
)

// Decision is the outcome of a policy evaluation.
type Decision string

const (
	Allow           Decision = "ALLOW"
	Deny            Decision = "DENY"
	RequireApproval Decision = "REQUIRE_APPROVAL"
)

// worse returns whichever of a, b is more restrictive: DENY > REQUIRE_APPROVAL > ALLOW.
func worse(a, b Decision) Decision {
	rank := map[Decision]int{Allow: 0, RequireApproval: 1, Deny: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ApprovalRequirement carries extra context when Decision == RequireApproval.
type ApprovalRequirement struct {
	Reason string `json:"reason"`
}

// PolicyDecision is the full, loggable result of an evaluation.
type PolicyDecision struct {
	Decision            Decision             `json:"decision"`
	ReasonCodes         []string             `json:"reason_codes"`
	PolicyVersion       string               `json:"policy_version"`
	ApprovalRequirement *ApprovalRequirement `json:"approval_requirement,omitempty"`
	EvaluatedAt         time.Time            `json:"evaluated_at"`
}

// merge combines two decisions into one, taking the more restrictive
// verdict and concatenating reason codes. Used to compose sub-evaluations
// (merchant, category, amount, payment source, ...) into one composite
// result for EvaluatePurchaseIntent/EvaluatePayment.
func merge(results ...*PolicyDecision) *PolicyDecision {
	out := &PolicyDecision{Decision: Allow, EvaluatedAt: time.Now().UTC()}
	for _, r := range results {
		if r == nil {
			continue
		}
		out.Decision = worse(out.Decision, r.Decision)
		out.ReasonCodes = append(out.ReasonCodes, r.ReasonCodes...)
		if r.PolicyVersion != "" {
			out.PolicyVersion = r.PolicyVersion
		}
		if r.ApprovalRequirement != nil {
			out.ApprovalRequirement = r.ApprovalRequirement
		}
	}
	if out.Decision == RequireApproval && out.ApprovalRequirement == nil {
		out.ApprovalRequirement = &ApprovalRequirement{Reason: "policy requires explicit user approval"}
	}
	return out
}

// Input is the full context needed to evaluate a purchase. Callers
// (internal/app/policy_service) assemble this from persisted state — the
// agent/LLM never supplies these fields directly.
type Input struct {
	UserID  string
	AgentID string

	Merchant         string
	Category         string
	AmountMinorUnits int64
	Currency         string

	PaymentProfile  string // alias, e.g. "payment:personal"
	ShippingProfile string // alias, e.g. "shipping:home"
	International   bool

	// SpendTodayMinorUnits is the user's spend-so-far today in the same
	// currency, supplied by the caller from the authoritative ledger. The
	// provider does not query storage itself — it is a pure function of its
	// input, which is what makes it independently unit-testable and
	// impossible for an agent to influence except through real, already-
	// audited state.
	SpendTodayMinorUnits int64

	// CryptoRecipient and AmountUSDC are populated only when the resolved
	// PaymentSource is a non-custodial crypto rail (SourceWallet /
	// SourceStablecoinAccount) — a wallet-address-or-x402-URL recipient and
	// a USDC decimal amount, not a merchant name and INR amount. LocalProvider
	// evaluates these with its own crypto-rail rules (AllowedCryptoRecipients,
	// MaxCryptoTxUSDC, CryptoConfirmThresholdUSDC — see rules.go); an ordinary
	// merchant/INR purchase leaves these fields empty.
	CryptoRecipient string
	AmountUSDC      string
}

// Provider is the policy/authorization abstraction — LocalProvider is
// Algebra's own deterministic engine and the only implementation; nothing
// else in Algebra talks to a policy engine directly.
type Provider interface {
	// EvaluatePurchaseIntent is the composite check run once a quote is
	// selected: merchant + category + amount + payment source + shipping,
	// merged into one decision.
	EvaluatePurchaseIntent(ctx context.Context, in Input) (*PolicyDecision, error)

	// EvaluatePayment is the composite check run immediately before
	// authorization: amount + payment source only (merchant/category were
	// already cleared at intent time; this catches a payment-source switch).
	EvaluatePayment(ctx context.Context, in Input) (*PolicyDecision, error)

	EvaluateMerchant(ctx context.Context, merchant string) (*PolicyDecision, error)
	EvaluateAmount(ctx context.Context, amountMinorUnits int64, currency string, spendTodayMinorUnits int64) (*PolicyDecision, error)
	EvaluateCategory(ctx context.Context, category string) (*PolicyDecision, error)
	EvaluatePaymentSource(ctx context.Context, paymentProfile string) (*PolicyDecision, error)

	// Version identifies the active policy/ruleset for audit trails.
	Version() string
}
