// Package quote defines CheckoutQuote — the normalized, merchant-confirmed
// price breakdown that Algebra treats as financial truth. A search-engine or
// catalog price is never authoritative (mandate §15/§18); only a
// CheckoutQuote retrieved from a MerchantConnector immediately before
// authorization is.
package quote

import (
	"time"

	"github.com/project-algebra/algebra/internal/domain/money"
)

// OfferType distinguishes offers that change what is actually charged from
// offers that don't. Mandate §17: "Do not pretend cashback reduces the
// amount actually charged."
type OfferType string

const (
	OfferImmediateDiscount OfferType = "IMMEDIATE_DISCOUNT"
	OfferCashback          OfferType = "CASHBACK"
	OfferConditional       OfferType = "CONDITIONAL_OFFER"
)

// Offer is one discount/cashback/conditional line called out separately in
// the quote breakdown, in addition to the aggregate fields on CheckoutQuote.
type Offer struct {
	Type        OfferType    `json:"type"`
	Description string       `json:"description"`
	Amount      money.Amount `json:"amount"`
	Code        string       `json:"code,omitempty"`
}

// Item is one line of the merchant's checkout basket.
type Item struct {
	MerchantProductID string       `json:"merchant_product_id"`
	Name              string       `json:"name"`
	Quantity          int          `json:"quantity"`
	UnitPrice         money.Amount `json:"unit_price"`
}

// CheckoutQuote is the normalized, merchant-confirmed price breakdown for a
// cart. FinalPayable is what will actually be charged; EffectiveCost is an
// informational number for comparison shopping that nets out cashback —
// never used as the authorization amount.
type CheckoutQuote struct {
	QuoteID string `json:"quote_id"`
	// CartID is the merchant connector's own cart identifier, needed to
	// refresh this quote (re-call GetCheckoutQuote) immediately before
	// authorization. It round-trips through Postgres storage like any other
	// field here — the boundary that keeps it out of agent-facing MCP/REST
	// responses is the transport layer's own explicit response DTOs
	// (internal/mcpserver, internal/api/v1), not a json struct tag. A
	// struct tag controls encoding/json, not who gets to call a Go method;
	// relying on it here would make "internal-only" one accidental
	// `json.Marshal(quote)` away from leaking.
	CartID   string `json:"cart_id"`
	Merchant string `json:"merchant"`
	Items    []Item `json:"items"`

	Subtotal       money.Amount `json:"subtotal"`
	ItemDiscounts  money.Amount `json:"item_discounts"`
	CouponDiscount money.Amount `json:"coupon_discount"`
	BankOffer      money.Amount `json:"bank_offer"`
	CardOffer      money.Amount `json:"card_offer"`
	Cashback       money.Amount `json:"cashback"`
	DeliveryFee    money.Amount `json:"delivery_fee"`
	HandlingFee    money.Amount `json:"handling_fee"`
	PlatformFee    money.Amount `json:"platform_fee"`
	Tax            money.Amount `json:"tax"`
	OtherFee       money.Amount `json:"other_fee"`

	// FinalPayable and EffectiveCost are computed by Recompute; callers
	// should not set them directly.
	FinalPayable  money.Amount `json:"final_payable"`
	EffectiveCost money.Amount `json:"effective_cost"`

	Offers                    []Offer  `json:"offers,omitempty"`
	PaymentSourceRequirements []string `json:"payment_source_requirements,omitempty"`

	DeliveryETA *time.Time `json:"delivery_eta,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RetrievedAt time.Time  `json:"retrieved_at"`
}

// Recompute derives FinalPayable and EffectiveCost from the component
// fields. FinalPayable NEVER subtracts cashback — cashback is paid back
// later, outside the transaction, and pretending otherwise would understate
// what the user is actually charged.
func (q *CheckoutQuote) Recompute() {
	cur := q.Subtotal.Currency
	zero := money.Amount{Currency: cur}

	final := q.Subtotal
	final = final.Sub(orZero(q.ItemDiscounts, zero))
	final = final.Sub(orZero(q.CouponDiscount, zero))
	final = final.Sub(orZero(q.BankOffer, zero))
	final = final.Sub(orZero(q.CardOffer, zero))
	final = final.Add(orZero(q.DeliveryFee, zero))
	final = final.Add(orZero(q.HandlingFee, zero))
	final = final.Add(orZero(q.PlatformFee, zero))
	final = final.Add(orZero(q.Tax, zero))
	final = final.Add(orZero(q.OtherFee, zero))
	q.FinalPayable = final

	// EffectiveCost is comparison-shopping information only: what the
	// purchase "really" costs once cashback eventually lands. It must never
	// be used as an authorization amount.
	q.EffectiveCost = final.Sub(orZero(q.Cashback, zero))
}

func orZero(a, zero money.Amount) money.Amount {
	if a.Currency == "" {
		return zero
	}
	return a
}

// IsExpired reports whether the quote is no longer safe to authorize
// against and must be refreshed.
func (q *CheckoutQuote) IsExpired(now time.Time) bool {
	return now.After(q.ExpiresAt)
}

// DriftedBeyondTolerance reports whether refreshed's FinalPayable differs
// from q's by more than tolerance, in which case execution must stop and
// the intent must move to REAPPROVAL_REQUIRED rather than silently charging
// a different amount than what the user approved (mandate §18).
func (q *CheckoutQuote) DriftedBeyondTolerance(refreshed *CheckoutQuote, tolerance money.Amount) bool {
	return !q.FinalPayable.WithinTolerance(refreshed.FinalPayable, tolerance)
}
