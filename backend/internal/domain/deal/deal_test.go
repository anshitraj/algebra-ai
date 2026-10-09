package deal

import (
	"testing"
	"time"
)

func TestBankOfferEstimateDiscount(t *testing.T) {
	pct := BankOffer{DiscountPercent: 10, MaxDiscountMinorUnits: 125000, MinOrderMinorUnits: 500000}
	flat := BankOffer{FlatDiscountMinorUnits: 150000, MinOrderMinorUnits: 1000000}
	cases := []struct {
		name  string
		offer BankOffer
		price int64
		want  int64
	}{
		{"unknown price", pct, 0, 0},
		{"under minimum order", pct, 499900, 0},
		{"percent under cap", pct, 800000, 80000},
		{"percent capped", pct, 5000000, 125000},
		{"flat", flat, 1200000, 150000},
		{"flat under minimum", flat, 900000, 0},
		{"never more than the price", BankOffer{FlatDiscountMinorUnits: 5000}, 3000, 3000},
	}
	for _, c := range cases {
		if got := c.offer.EstimateDiscount(c.price); got != c.want {
			t.Errorf("%s: EstimateDiscount(%d) = %d, want %d", c.name, c.price, got, c.want)
		}
	}
}

func TestSameBank(t *testing.T) {
	match := [][2]string{
		{"HDFC", "HDFC Bank"},
		{"hdfc bank credit card", "HDFC Bank"},
		{"SBI", "State Bank of India"},
		{"sbi card", "State Bank of India"},
		{"Kotak", "Kotak Mahindra Bank"},
		{"BoB", "Bank of Baroda"},
		{"Amex", "American Express"},
		{"ICICI Bank", "icici"},
	}
	for _, m := range match {
		if !SameBank(m[0], m[1]) {
			t.Errorf("SameBank(%q, %q) = false, want true", m[0], m[1])
		}
	}
	noMatch := [][2]string{{"HDFC", "ICICI"}, {"", ""}, {"bank", "card"}, {"Axis", "AU Small Finance Bank"}}
	for _, m := range noMatch {
		if SameBank(m[0], m[1]) {
			t.Errorf("SameBank(%q, %q) = true, want false", m[0], m[1])
		}
	}
}

func TestDealActiveAt(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		name string
		d    Deal
		want bool
	}{
		{"no window", Deal{}, true},
		{"running", Deal{StartsAt: &past, EndsAt: &future}, true},
		{"not started", Deal{StartsAt: &future}, false},
		{"ended", Deal{EndsAt: &past}, false},
		{"ends exactly now", Deal{EndsAt: &now}, false},
	}
	for _, c := range cases {
		if got := c.d.ActiveAt(now); got != c.want {
			t.Errorf("%s: ActiveAt = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBankOfferDeal(t *testing.T) {
	o := BankOffer{
		Merchant: "flipkart", Bank: "HDFC Bank", Title: "10% off with HDFC Bank credit cards",
		DiscountPercent: 10, MaxDiscountMinorUnits: 125000, MinOrderMinorUnits: 500000,
		EndsAt: time.Date(2026, 10, 2, 18, 29, 59, 0, time.UTC), TermsURL: "https://www.flipkart.com/offers",
	}
	d := o.Deal(700000)
	if d.Kind != KindBankOffer || d.Source != SourceCurated || d.StartsAt != nil || d.EndsAt == nil {
		t.Fatalf("unexpected deal %+v", d)
	}
	if d.EstimatedDiscountMinorUnits != 70000 {
		t.Fatalf("estimate = %d, want 70000", d.EstimatedDiscountMinorUnits)
	}
}
