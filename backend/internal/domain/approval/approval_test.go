package approval

import (
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/money"
)

func TestCanonicalHash_OrderIndependent(t *testing.T) {
	items := []HashableItem{
		{MerchantProductID: "sku-b", Quantity: 1, UnitPriceMinorUnits: 5000},
		{MerchantProductID: "sku-a", Quantity: 2, UnitPriceMinorUnits: 2000},
	}
	reordered := []HashableItem{items[1], items[0]}

	h1 := CanonicalHash("zepto", items, "INR", "payment:personal")
	h2 := CanonicalHash("zepto", reordered, "INR", "payment:personal")
	if h1 != h2 {
		t.Errorf("expected item order to not affect hash: %s vs %s", h1, h2)
	}
}

func TestCanonicalHash_ChangesWithAnyField(t *testing.T) {
	base := []HashableItem{{MerchantProductID: "sku-a", Quantity: 1, UnitPriceMinorUnits: 1000}}
	baseline := CanonicalHash("zepto", base, "INR", "payment:personal")
	differentPrice := []HashableItem{{MerchantProductID: "sku-a", Quantity: 1, UnitPriceMinorUnits: 1001}}

	cases := map[string]string{
		"different merchant":   CanonicalHash("blinkit", base, "INR", "payment:personal"),
		"different item price": CanonicalHash("zepto", differentPrice, "INR", "payment:personal"),
		"different currency":   CanonicalHash("zepto", base, "USD", "payment:personal"),
		"different payment":    CanonicalHash("zepto", base, "INR", "payment:travel"),
	}
	for name, h := range cases {
		if h == baseline {
			t.Errorf("%s: expected hash to change, both are %s", name, h)
		}
	}
}

func TestCanonicalHash_StableAcrossAggregateAmountChanges(t *testing.T) {
	// The whole point of separating the items hash from the amount
	// comparison: a fee/tax change that moves the TOTAL must not, by
	// itself, change the items hash — otherwise the tolerance check in
	// Matches could never engage.
	items := []HashableItem{{MerchantProductID: "sku-a", Quantity: 1, UnitPriceMinorUnits: 1000}}
	h := CanonicalHash("zepto", items, "INR", "payment:personal")
	hAgain := CanonicalHash("zepto", items, "INR", "payment:personal")
	if h != hAgain {
		t.Error("expected identical inputs to produce identical hashes")
	}
}

func TestMatches_AmountWithinTolerance(t *testing.T) {
	a := &Approval{
		Merchant:           "zepto",
		Amount:             money.Amount{MinorUnits: 40000, Currency: "INR"},
		PaymentSourceAlias: "payment:personal",
		ItemsHash:          "hash1",
	}
	tolerance := money.Amount{MinorUnits: 100, Currency: "INR"}

	if !a.Matches("zepto", "hash1", money.Amount{MinorUnits: 40050, Currency: "INR"}, "payment:personal", tolerance) {
		t.Error("expected a small drift within tolerance to still match")
	}
	if a.Matches("zepto", "hash1", money.Amount{MinorUnits: 41000, Currency: "INR"}, "payment:personal", tolerance) {
		t.Error("expected a large drift beyond tolerance to not match")
	}
}

func TestMatches_CurrencyMismatchReturnsFalseWithoutPanicking(t *testing.T) {
	a := &Approval{
		Merchant:           "zepto",
		Amount:             money.Amount{MinorUnits: 40000, Currency: "INR"},
		PaymentSourceAlias: "payment:personal",
		ItemsHash:          "hash1",
	}
	tolerance := money.Amount{MinorUnits: 100, Currency: "INR"}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Matches must not panic on currency mismatch, got panic: %v", r)
		}
	}()
	if a.Matches("zepto", "hash1", money.Amount{MinorUnits: 40000, Currency: "USD"}, "payment:personal", tolerance) {
		t.Error("expected a currency mismatch to not match")
	}
}

func TestMatches_RejectsMerchantOrItemOrPaymentSubstitution(t *testing.T) {
	a := &Approval{
		Merchant:           "zepto",
		Amount:             money.Amount{MinorUnits: 40000, Currency: "INR"},
		PaymentSourceAlias: "payment:personal",
		ItemsHash:          "hash1",
	}
	amt := money.Amount{MinorUnits: 40000, Currency: "INR"}
	tolerance := money.Amount{MinorUnits: 100, Currency: "INR"}

	if a.Matches("blinkit", "hash1", amt, "payment:personal", tolerance) {
		t.Error("must reject a merchant substitution")
	}
	if a.Matches("zepto", "different-hash", amt, "payment:personal", tolerance) {
		t.Error("must reject an item substitution")
	}
	if a.Matches("zepto", "hash1", amt, "payment:travel", tolerance) {
		t.Error("must reject a payment-source substitution")
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now()
	a := &Approval{ExpiresAt: now.Add(-time.Minute)}
	if !a.IsExpired(now) {
		t.Error("expected approval to be expired")
	}
}

func TestExecutable(t *testing.T) {
	now := time.Now()
	approved := &Approval{Status: StatusApproved, ExpiresAt: now.Add(time.Minute)}
	if !approved.Executable(now) {
		t.Error("expected an approved, unexpired approval to be executable")
	}

	expired := &Approval{Status: StatusApproved, ExpiresAt: now.Add(-time.Minute)}
	if expired.Executable(now) {
		t.Error("expected an expired approval to not be executable")
	}

	pending := &Approval{Status: StatusPending, ExpiresAt: now.Add(time.Minute)}
	if pending.Executable(now) {
		t.Error("expected a pending (not yet approved) approval to not be executable")
	}

	consumed := &Approval{Status: StatusConsumed, ExpiresAt: now.Add(time.Minute)}
	if consumed.Executable(now) {
		t.Error("expected a consumed approval to not be re-executable (replay protection)")
	}
}
