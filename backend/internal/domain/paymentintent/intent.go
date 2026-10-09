// Package paymentintent defines AgenticPaymentIntent: the primitive a
// tenant's agent uses to request a single agentic payment. It is deliberately
// separate from internal/domain/intent.PurchaseIntent — PurchaseIntent is
// commerce/discovery-shaped (items, merchant search, quotes), right for
// Algebra's own consumer reference app's "find me headphones" flow. A
// tenant's agent typically already knows the merchant and amount (no
// discovery step at all — "spend $73 at this specific merchant"), which
// PurchaseIntent's discovery-shaped state machine is a poor fit for. What IS
// shared is the pattern: a single transition-table allow-list plus one
// enforcement function, copied from internal/domain/intent because that is
// the most rigorously-enforced state machine in the codebase.
//
// An AgenticPaymentIntent never carries a raw payment credential — only a
// PaymentSourceAlias (a reference into internal/domain/payment.PaymentSource,
// the same alias-scoped mechanism the commerce flow already uses) and,
// after execution, an opaque ProviderTransactionID. See
// internal/domain/paymentprovider for how a credential actually moves.
package paymentintent

import "time"

// AgenticPaymentIntent is a tenant's agent's request to move a bounded
// amount of money to a merchant, evaluated against the tenant's own
// persisted policy and executed through a internal/domain/paymentprovider.
type AgenticPaymentIntent struct {
	ID       string `json:"payment_intent_id"`
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`  // the tenant's end user this spend is on behalf of
	AgentID  string `json:"agent_id"` // the requesting agent

	Purpose        string `json:"purpose,omitempty"` // free-text, agent-supplied — audit/UI context only, never used by policy
	Merchant       string `json:"merchant"`
	MerchantDomain string `json:"merchant_domain,omitempty"`
	Category       string `json:"category,omitempty"` // policy dimension — e.g. "gambling", "groceries"; see policy.Input.Category
	International  bool   `json:"international,omitempty"`

	AmountMinorUnits    int64  `json:"amount_minor_units"`
	Currency            string `json:"currency"`
	ToleranceMinorUnits int64  `json:"tolerance_minor_units,omitempty"`

	ProductRef         string `json:"product_ref,omitempty"` // optional order/SKU reference the tenant's own system already has
	PaymentSourceAlias string `json:"payment_source_alias"`

	// RequestedCapability is an optional hint at which payment rail the
	// caller expects (e.g. "card_purchase") — internal/app's capability
	// resolver decides the actual rail; this is never trusted as an
	// authorization by itself.
	RequestedCapability string `json:"requested_capability,omitempty"`

	Status State `json:"status"`

	// PolicyVersion records which policy.Rules version evaluated this
	// intent — the same "explain by showing what was actually recorded"
	// principle internal/domain/intent's PolicyDecision follows.
	PolicyVersion string `json:"policy_version,omitempty"`

	// Provider* are populated once execution completes — the authoritative
	// result from the payment provider, never fabricated locally (mandate
	// carried over from internal/app/order_service.go: Algebra reports what
	// a provider actually confirmed, not what it hopes happened).
	ProviderTransactionID string `json:"provider_transaction_id,omitempty"`
	ProviderStatus        string `json:"provider_status,omitempty"`
	FinalAmountMinorUnits int64  `json:"final_amount_minor_units,omitempty"`
	FinalCurrency         string `json:"final_currency,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// Metadata carries small, non-sensitive extra context — never a payment
	// credential, address, or other private-profile data.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// New constructs a DRAFT AgenticPaymentIntent. It does not persist anything
// or evaluate policy.
func New(id, tenantID, userID, agentID, merchant string, amountMinorUnits int64, currency, paymentSourceAlias string, now time.Time) *AgenticPaymentIntent {
	return &AgenticPaymentIntent{
		ID: id, TenantID: tenantID, UserID: userID, AgentID: agentID,
		Merchant: merchant, AmountMinorUnits: amountMinorUnits, Currency: currency,
		PaymentSourceAlias: paymentSourceAlias,
		Status:             StateDraft,
		CreatedAt:          now, UpdatedAt: now,
	}
}

// ApplyTransition validates and applies a state change in place, returning
// the previous state for audit-event construction — the only method that
// changes Status. See state_machine.go.
func (p *AgenticPaymentIntent) ApplyTransition(to State, now time.Time) (previous State, err error) {
	previous = p.Status
	next, err := Transition(p.Status, to)
	if err != nil {
		return previous, err
	}
	p.Status = next
	p.UpdatedAt = now
	return previous, nil
}
