// Package commerceprofile defines CommerceProfile — a portable, low-
// sensitivity preference profile the shopping agent consults before asking
// clarifying questions, plus a pointer to the user's default shipping/
// payment aliases (never the address/payment data itself — that stays
// behind internal/domain/privacy.Resolver, resolved server-side only, and
// is never exposed to an agent).
//
// Preferences (clothing size, color, style, dietary flags, ...) sit at a
// deliberately different sensitivity tier from address/payment data: safe
// to hand directly to an agent/LLM, unlike a ShippingProfile or
// BillingProfile. There is no rigid per-category Go schema here on
// purpose — Preferences is a flexible category->attributes map, the same
// "don't invent a taxonomy" pattern PurchaseIntent.Metadata and
// audit.Event.Metadata already use elsewhere in this codebase, since no
// product category beyond what a merchant connector's catalog actually
// supports is real in this system yet.
package commerceprofile

import "time"

// CommerceProfile is one row per user — not per-alias, not versioned.
type CommerceProfile struct {
	UserID string `json:"user_id"`

	// DefaultShippingAlias/DefaultPaymentAlias point at an existing
	// privacy.ProfileShipping alias / payment_sources alias — this struct
	// never stores an address or payment credential itself.
	DefaultShippingAlias string `json:"default_shipping_alias,omitempty"`
	DefaultPaymentAlias  string `json:"default_payment_alias,omitempty"`

	// Preferences is category -> flat attribute bag, e.g.
	// {"clothing": {"usual_size": "L", "preferred_colors": ["black","navy"]}}.
	Preferences map[string]map[string]any `json:"preferences"`

	UpdatedAt time.Time `json:"updated_at"`
}

// Empty returns a valid, empty profile for userID — the default for a user
// who hasn't set anything yet, so callers never need to special-case "no
// profile exists."
func Empty(userID string) *CommerceProfile {
	return &CommerceProfile{UserID: userID, Preferences: map[string]map[string]any{}}
}

// MergePreferences merges attributes into one category in place, adding or
// overwriting individual keys without touching other categories or other
// keys within the same category — "the profile gets better through usage,"
// not "the last write wins for everything."
func (p *CommerceProfile) MergePreferences(category string, attributes map[string]any) {
	if p.Preferences == nil {
		p.Preferences = map[string]map[string]any{}
	}
	existing, ok := p.Preferences[category]
	if !ok {
		existing = map[string]any{}
	}
	for k, v := range attributes {
		existing[k] = v
	}
	p.Preferences[category] = existing
}
