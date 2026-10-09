// Package approval implements the binding between what a user approved and
// what may actually be executed. An Approval binds to an exact merchant,
// item set, amount (within tolerance), currency, and payment source — if
// any of those materially change, the approval is invalid and execution
// must stop (mandate §29/§30).
package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/money"
)

// Status is the lifecycle of an approval.
type Status string

const (
	StatusPending            Status = "PENDING"
	StatusApproved           Status = "APPROVED"
	StatusRejected           Status = "REJECTED"
	StatusExpired            Status = "EXPIRED"
	StatusConsumed           Status = "CONSUMED" // execution has used this approval; cannot be reused
	StatusReapprovalRequired Status = "REAPPROVAL_REQUIRED"
)

// HashableItem is the minimal per-item shape the approval hash binds to.
type HashableItem struct {
	MerchantProductID   string
	Quantity            int
	UnitPriceMinorUnits int64
}

// Approval records the exact terms a user consented to, and is the only
// thing execution is allowed to act on.
//
// Exactly one of IntentID or AgenticPaymentIntentID is set — an Approval
// binds to either a commerce-flow PurchaseIntent (internal/domain/intent,
// QuoteID/ItemsHash derived from real cart items) or a tenant's
// AgenticPaymentIntent (internal/domain/paymentintent, which has no
// items/quote — ItemsHash is computed with an empty item list, still
// binding merchant+currency+payment-alias, and QuoteID is left empty).
// This reuses the same CanonicalHash/Matches/MarkConsumed machinery for
// both rather than building a second approval mechanism.
type Approval struct {
	ID                     string
	IntentID               string
	AgenticPaymentIntentID string
	QuoteID                string
	UserID                 string
	AgentID                string

	Merchant           string
	Amount             money.Amount
	PaymentSourceAlias string
	ItemsHash          string

	Status               Status
	AuthenticationMethod string

	CreatedAt time.Time
	DecidedAt *time.Time
	ExpiresAt time.Time
}

// CanonicalHash computes a deterministic SHA-256 hex digest over merchant +
// items (including each item's unit price) + currency + payment alias.
// Item order does not affect the result — items are sorted by
// MerchantProductID first — so two callers building the same cart in a
// different order still bind to the same approval.
//
// The TOTAL amount is deliberately NOT part of this hash: it is compared
// separately, with tolerance, in Matches. Binding amount into the hash
// would make the tolerance check meaningless, since any fee/tax drift
// (however small) would change the hash before the tolerance comparison
// ever ran. A change to any item's own unit price, by contrast, DOES change
// the hash — per-item price is exact-match, only aggregate fees/tax/
// delivery drift gets tolerance.
func CanonicalHash(merchant string, items []HashableItem, currency, paymentAlias string) string {
	sorted := make([]HashableItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].MerchantProductID < sorted[j].MerchantProductID })

	var b strings.Builder
	fmt.Fprintf(&b, "merchant=%s;", merchant)
	for _, it := range sorted {
		fmt.Fprintf(&b, "item=%s:%d:%d;", it.MerchantProductID, it.Quantity, it.UnitPriceMinorUnits)
	}
	fmt.Fprintf(&b, "currency=%s;payment=%s", currency, paymentAlias)

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Matches reports whether the given execution-time terms still fall within
// what this approval authorized. Amount may drift by tolerance (a quote
// refresh can legitimately move fees by a few paise); merchant, items hash,
// currency, and payment alias must match exactly. Currency is checked
// explicitly (rather than left to money.Amount.WithinTolerance) so a
// currency mismatch returns false instead of panicking.
func (a *Approval) Matches(merchant, itemsHash string, amount money.Amount, paymentAlias string, tolerance money.Amount) bool {
	if a.Merchant != merchant {
		return false
	}
	if a.ItemsHash != itemsHash {
		return false
	}
	if a.PaymentSourceAlias != paymentAlias {
		return false
	}
	if a.Amount.Currency != amount.Currency || a.Amount.Currency != tolerance.Currency {
		return false
	}
	if !a.Amount.WithinTolerance(amount, tolerance) {
		return false
	}
	return true
}

func (a *Approval) IsExpired(now time.Time) bool {
	return now.After(a.ExpiresAt)
}

// Executable reports whether this approval may currently be used to
// authorize execution: approved, not expired, not already consumed.
func (a *Approval) Executable(now time.Time) bool {
	return a.Status == StatusApproved && !a.IsExpired(now)
}
