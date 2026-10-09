package v1

import (
	"errors"
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/policy"
)

// The request body mirrors the WHO/WHAT/WHERE/HOW MUCH/WITH WHAT/UNDER
// WHAT CONDITIONS framing Algebra's own PolicyProvider is built on (see
// policy.Input's doc comment) — a third-party integrator describes a
// transaction the same way Algebra describes a purchase intent internally,
// just without an Algebra intent behind it.
type evaluateTransactionRequest struct {
	Who        whoPayload         `json:"who"`
	What       whatPayload        `json:"what"`
	Where      wherePayload       `json:"where"`
	HowMuch    howMuchPayload     `json:"how_much"`
	WithWhat   withWhatPayload    `json:"with_what"`
	Conditions *conditionsPayload `json:"conditions"`
}

type whoPayload struct {
	UserRef string `json:"user_ref"`
}

type whatPayload struct {
	Category string `json:"category"`
}

type wherePayload struct {
	Merchant      string `json:"merchant"`
	International bool   `json:"international,omitempty"`
}

type howMuchPayload struct {
	AmountMinorUnits     int64  `json:"amount_minor_units"`
	Currency             string `json:"currency"`
	SpendTodayMinorUnits int64  `json:"spend_today_minor_units,omitempty"`
}

type withWhatPayload struct {
	PaymentRef string `json:"payment_ref"`
}

// conditionsPayload is the integrator's own budget/category/merchant
// policy — the exact shape of policy.Rules, minus the crypto-rail fields
// (Input.CryptoRecipient/AmountUSDC has no counterpart in this card/wallet
// -shaped request; an integrator evaluating a crypto-rail transaction is a
// distinct, not-yet-exposed use case).
type conditionsPayload struct {
	MaxPerTransactionMinorUnits   int64    `json:"max_per_transaction_minor_units,omitempty"`
	MaxPerDayMinorUnits           int64    `json:"max_per_day_minor_units,omitempty"`
	ApprovalThresholdMinorUnits   int64    `json:"approval_threshold_minor_units,omitempty"`
	AllowedMerchants              []string `json:"allowed_merchants,omitempty"`
	BlockedMerchants              []string `json:"blocked_merchants,omitempty"`
	BlockedCategories             []string `json:"blocked_categories,omitempty"`
	AllowedPaymentProfiles        []string `json:"allowed_payment_profiles,omitempty"`
	InternationalRequiresApproval bool     `json:"international_requires_approval,omitempty"`
}

func (c conditionsPayload) toRules() policy.Rules {
	return policy.Rules{
		MaxPerTransactionMinorUnits:   c.MaxPerTransactionMinorUnits,
		MaxPerDayMinorUnits:           c.MaxPerDayMinorUnits,
		ApprovalThresholdMinorUnits:   c.ApprovalThresholdMinorUnits,
		AllowedMerchants:              c.AllowedMerchants,
		BlockedMerchants:              c.BlockedMerchants,
		BlockedCategories:             c.BlockedCategories,
		AllowedPaymentProfiles:        c.AllowedPaymentProfiles,
		InternationalRequiresApproval: c.InternationalRequiresApproval,
	}
}

// evaluateTransaction is POST /api/v1/policy/evaluate-transaction — the
// standalone third-party surface. Requires an Integrator bearer token, not
// an agent token: an integrator has no shopping permissions, it can only
// ask "should this transaction happen."
func (a *API) evaluateTransaction(w http.ResponseWriter, r *http.Request) {
	integ, err := a.resolveIntegrator(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req evaluateTransactionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Conditions == nil {
		writeError(w, errors.New("missing \"conditions\" — Algebra does not assume a default budget policy for a third-party integrator; send at least an empty {} to explicitly allow everything"))
		return
	}

	decision, err := a.b.TransactionPolicy.EvaluateTransaction(r.Context(), integ.ID, app.EvaluateTransactionInput{
		UserRef:              req.Who.UserRef,
		Category:             req.What.Category,
		Merchant:             req.Where.Merchant,
		International:        req.Where.International,
		AmountMinorUnits:     req.HowMuch.AmountMinorUnits,
		Currency:             req.HowMuch.Currency,
		SpendTodayMinorUnits: req.HowMuch.SpendTodayMinorUnits,
		PaymentRef:           req.WithWhat.PaymentRef,
		Rules:                req.Conditions.toRules(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decisionResponse{Decision: string(decision.Decision), ReasonCodes: decision.ReasonCodes, PolicyVersion: decision.PolicyVersion})
}
