// Package deal holds the normalized shape of a discount a user can get on a
// merchant: a store's own published offer or deal, a price drop on one item,
// or a bank/card offer. Deals are informational — Algebra never applies one,
// never promises one, and never invents a coupon code. Every Deal names the
// source it came from so an agent can say how sure it is.
package deal

import (
	"strings"
	"time"
	"unicode"
)

// Kind separates what the user has to do to get a deal.
type Kind string

const (
	// KindStoreOffer is a promotion the merchant publishes through its own
	// API (e.g. Flipkart's affiliate offers feed): usually a category or
	// brand sale, applied automatically on the merchant's site.
	KindStoreOffer Kind = "store_offer"
	// KindItemDeal is a live discount on one specific product, as the
	// merchant's catalog API reports it (e.g. Amazon's savings vs. the list
	// or was price, or a time-boxed Lightning/Prime deal).
	KindItemDeal Kind = "item_deal"
	// KindBankOffer is an instant discount for paying with a particular
	// bank's card, taken from the merchant's published terms by the operator
	// (see BankOffer). The user gets it by picking that card at checkout.
	KindBankOffer Kind = "bank_offer"
)

// Source says where a Deal came from — never "the model thought so".
type Source string

const (
	SourceFlipkartAffiliate Source = "flipkart_affiliate_api"
	SourceAmazonCreators    Source = "amazon_creators_api"
	SourceCurated           Source = "curated_bank_offers"
)

// Deal is one discount, normalized across merchants. Only the fields that
// make sense for its Kind are set.
type Deal struct {
	Merchant    string `json:"merchant"`
	Kind        Kind   `json:"kind"`
	Source      Source `json:"source"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	Category    string `json:"category,omitempty"`

	// Item deals: the product's current price and what it saves against
	// the merchant's own reference price (BasisLabel, e.g. "M.R.P.").
	PriceMinorUnits   int64  `json:"price_minor_units,omitempty"`
	WasMinorUnits     int64  `json:"was_minor_units,omitempty"`
	SavingsMinorUnits int64  `json:"savings_minor_units,omitempty"`
	SavingsPercent    int    `json:"savings_percent,omitempty"`
	BasisLabel        string `json:"basis_label,omitempty"`
	Currency          string `json:"currency,omitempty"`
	Badge             string `json:"badge,omitempty"`
	PrimeOnly         bool   `json:"prime_only,omitempty"`
	PercentClaimed    int    `json:"percent_claimed,omitempty"`

	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`

	// Bank offers only.
	Bank                   string   `json:"bank,omitempty"`
	CardTypes              []string `json:"card_types,omitempty"`
	DiscountPercent        int      `json:"discount_percent,omitempty"`
	FlatDiscountMinorUnits int64    `json:"flat_discount_minor_units,omitempty"`
	MaxDiscountMinorUnits  int64    `json:"max_discount_minor_units,omitempty"`
	MinOrderMinorUnits     int64    `json:"min_order_minor_units,omitempty"`
	// EstimatedDiscountMinorUnits is what the offer's published terms work
	// out to for the price the caller asked about. An estimate: the merchant
	// computes the real amount at checkout.
	EstimatedDiscountMinorUnits int64 `json:"estimated_discount_minor_units,omitempty"`
	// MatchesUserCard is true when Bank matches one of the banks the caller
	// said the user holds cards from.
	MatchesUserCard bool `json:"matches_user_card,omitempty"`
	// VerifiedAt is when the operator last checked the terms against the
	// merchant's own offer page.
	VerifiedAt string `json:"verified_at,omitempty"`
}

// ActiveAt reports whether now falls inside the deal's window. A deal with
// no window is treated as active — the merchant's feed said it's live.
func (d Deal) ActiveAt(now time.Time) bool {
	if d.StartsAt != nil && now.Before(*d.StartsAt) {
		return false
	}
	if d.EndsAt != nil && !now.Before(*d.EndsAt) {
		return false
	}
	return true
}

// BankOffer is one card/bank instant-discount offer, as an operator copied
// it from the merchant's published offer terms. Neither Amazon nor Flipkart
// publishes these through an API, so a person curates them; EndsAt and
// TermsURL are mandatory so a stale or unsourced offer can't be shown.
type BankOffer struct {
	Merchant  string   `json:"merchant"`
	Bank      string   `json:"bank"`
	CardTypes []string `json:"card_types,omitempty"` // e.g. "credit", "debit", "credit_emi"
	Title     string   `json:"title"`

	DiscountPercent        int   `json:"discount_percent,omitempty"`
	FlatDiscountMinorUnits int64 `json:"flat_discount_minor_units,omitempty"`
	MaxDiscountMinorUnits  int64 `json:"max_discount_minor_units,omitempty"`
	MinOrderMinorUnits     int64 `json:"min_order_minor_units,omitempty"`

	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	TermsURL   string    `json:"terms_url"`
	VerifiedAt string    `json:"verified_at,omitempty"`
	// Notes is for the operator's own bookkeeping; it is never shown.
	Notes string `json:"notes,omitempty"`
}

// EstimateDiscount applies the offer's published terms to a price. Returns 0
// when the price is unknown or under the minimum order.
func (o BankOffer) EstimateDiscount(priceMinor int64) int64 {
	if priceMinor <= 0 || priceMinor < o.MinOrderMinorUnits {
		return 0
	}
	d := o.FlatDiscountMinorUnits
	if o.DiscountPercent > 0 {
		d = priceMinor * int64(o.DiscountPercent) / 100
	}
	if o.MaxDiscountMinorUnits > 0 && d > o.MaxDiscountMinorUnits {
		d = o.MaxDiscountMinorUnits
	}
	if d > priceMinor {
		d = priceMinor
	}
	return d
}

// Deal converts the offer to the common shape, with the estimate for
// priceMinor (0 = unknown price).
func (o BankOffer) Deal(priceMinor int64) Deal {
	starts, ends := o.StartsAt, o.EndsAt
	d := Deal{
		Merchant:               o.Merchant,
		Kind:                   KindBankOffer,
		Source:                 SourceCurated,
		Title:                  o.Title,
		URL:                    o.TermsURL,
		Currency:               "INR",
		Bank:                   o.Bank,
		CardTypes:              o.CardTypes,
		DiscountPercent:        o.DiscountPercent,
		FlatDiscountMinorUnits: o.FlatDiscountMinorUnits,
		MaxDiscountMinorUnits:  o.MaxDiscountMinorUnits,
		MinOrderMinorUnits:     o.MinOrderMinorUnits,
		EndsAt:                 &ends,
		VerifiedAt:             o.VerifiedAt,
	}
	if !starts.IsZero() {
		d.StartsAt = &starts
	}
	d.EstimatedDiscountMinorUnits = o.EstimateDiscount(priceMinor)
	return d
}

// SameBank reports whether two bank names refer to the same bank, ignoring
// case, punctuation and the words "bank" and "card(s)": "HDFC", "hdfc bank"
// and "HDFC Bank credit card" all match.
func SameBank(a, b string) bool {
	na, nb := normalizeBank(a), normalizeBank(b)
	return na != "" && na == nb
}

var bankNoise = map[string]bool{"bank": true, "card": true, "cards": true, "credit": true, "debit": true, "ltd": true, "limited": true, "of": true, "the": true}

// bankAliases folds a bank's full name and its usual short form together,
// keyed by the normalized full name.
var bankAliases = map[string]string{
	"stateindia":      "sbi",
	"kotakmahindra":   "kotak",
	"baroda":          "bob",
	"punjabnational":  "pnb",
	"americanexpress": "amex",
	"idfcfirst":       "idfc",
	"ausmallfinance":  "au",
}

func normalizeBank(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	kept := words[:0]
	for _, w := range words {
		if !bankNoise[w] {
			kept = append(kept, w)
		}
	}
	n := strings.Join(kept, "")
	if alias, ok := bankAliases[n]; ok {
		return alias
	}
	return n
}
