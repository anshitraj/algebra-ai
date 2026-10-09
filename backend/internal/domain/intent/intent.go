// Package intent defines the PurchaseIntent primitive and its state machine.
// A PurchaseIntent is the normalized representation of what a user asked
// for. An AI agent may create one; it may never mutate its state directly —
// all transitions go through Transition (state_machine.go) and are called
// only from internal/app services.
package intent

import "time"

// Item is one line item the user wants — a free-text query plus quantity,
// not yet resolved to a specific merchant product.
type Item struct {
	Query    string `json:"query"`
	Quantity int    `json:"quantity"`

	// ProductID is set once discovery has matched this item to a normalized
	// product candidate (internal/domain/product, added in discovery).
	ProductID string `json:"product_id,omitempty"`
}

// Constraints bounds what the agent is allowed to spend and how the order
// may be fulfilled. MaxTotalMinorUnits/Currency are authoritative limits —
// policy evaluation and quote selection must never exceed them.
type Constraints struct {
	MaxTotalMinorUnits int64    `json:"max_total_minor_units"`
	Currency           string   `json:"currency"`
	DeliveryProfile    string   `json:"delivery_profile,omitempty"` // privacy alias, e.g. "home"
	PaymentProfile     string   `json:"payment_profile,omitempty"`  // privacy alias, e.g. "personal"
	PreferredMerchants []string `json:"preferred_merchants,omitempty"`
	ExcludedMerchants  []string `json:"excluded_merchants,omitempty"`
	// Category is an optional agent-supplied hint (e.g. "groceries",
	// "gift_cards") used by policy category checks. It is a hint, not
	// ground truth — a real catalog-derived category would take precedence
	// once product normalization (mandate §16) is built out.
	Category string `json:"category,omitempty"`
	// International tells policy whether this purchase targets a merchant
	// outside the user's home country; supplied by discovery/connector
	// metadata once that classification exists. Defaults to false.
	International bool `json:"international,omitempty"`
}

// PurchaseIntent is the normalized representation of a user's shopping
// instruction, and the root object the whole commerce flow hangs off of.
type PurchaseIntent struct {
	ID          string      `json:"intent_id"`
	UserID      string      `json:"user_id"`
	AgentID     string      `json:"agent_id"`
	Status      State       `json:"status"`
	Items       []Item      `json:"items"`
	Constraints Constraints `json:"constraints"`

	SelectedQuoteID string `json:"selected_quote_id,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// Metadata carries non-sensitive, low-cardinality context (e.g. the raw
	// user instruction for audit purposes). It must never hold payment
	// credentials, addresses, or other private-profile data — those live
	// behind internal/domain/privacy and are referenced only by alias.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// New constructs a DRAFT PurchaseIntent. It does not persist anything.
func New(id, userID, agentID string, items []Item, constraints Constraints, now time.Time) *PurchaseIntent {
	return &PurchaseIntent{
		ID:          id,
		UserID:      userID,
		AgentID:     agentID,
		Status:      StateDraft,
		Items:       items,
		Constraints: constraints,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ApplyTransition validates and applies a state change in place, returning
// the previous state for audit-event construction. It is the only method on
// PurchaseIntent that changes Status — see state_machine.go.
func (p *PurchaseIntent) ApplyTransition(to State, now time.Time) (previous State, err error) {
	previous = p.Status
	next, err := Transition(p.Status, to)
	if err != nil {
		return previous, err
	}
	p.Status = next
	p.UpdatedAt = now
	return previous, nil
}
