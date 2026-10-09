package quote

import (
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/money"
)

func inr(minorUnits int64) money.Amount {
	return money.Amount{MinorUnits: minorUnits, Currency: "INR"}
}

func TestRecompute_BasicArithmetic(t *testing.T) {
	q := &CheckoutQuote{
		Subtotal:       inr(50000), // 500.00
		ItemDiscounts:  inr(2000),  // -20.00
		CouponDiscount: inr(3000),  // -30.00
		BankOffer:      inr(1000),  // -10.00
		DeliveryFee:    inr(4000),  // +40.00
		Tax:            inr(500),   // +5.00
	}
	q.Recompute()

	// 500 - 20 - 30 - 10 + 40 + 5 = 485
	want := inr(48500)
	if q.FinalPayable != want {
		t.Errorf("FinalPayable = %v, want %v", q.FinalPayable, want)
	}
}

func TestRecompute_CashbackNeverReducesFinalPayable(t *testing.T) {
	q := &CheckoutQuote{
		Subtotal: inr(40000),
		Cashback: inr(10000), // must NOT lower what's actually charged
	}
	q.Recompute()

	if q.FinalPayable != inr(40000) {
		t.Errorf("cashback must not reduce FinalPayable: got %v", q.FinalPayable)
	}
	// EffectiveCost is comparison-only and DOES net out cashback.
	if q.EffectiveCost != inr(30000) {
		t.Errorf("EffectiveCost should net out cashback: got %v", q.EffectiveCost)
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now()
	q := &CheckoutQuote{ExpiresAt: now.Add(-time.Second)}
	if !q.IsExpired(now) {
		t.Error("expected quote to be expired")
	}
	q2 := &CheckoutQuote{ExpiresAt: now.Add(time.Minute)}
	if q2.IsExpired(now) {
		t.Error("expected quote to not be expired")
	}
}

func TestDriftedBeyondTolerance(t *testing.T) {
	original := &CheckoutQuote{Subtotal: inr(40000)}
	original.Recompute()

	withinRange := &CheckoutQuote{Subtotal: inr(40050)} // +0.50
	withinRange.Recompute()
	if original.DriftedBeyondTolerance(withinRange, inr(100)) {
		t.Error("a 0.50 drift within a 1.00 tolerance must not require reapproval")
	}

	tooMuch := &CheckoutQuote{Subtotal: inr(41000)} // +10.00
	tooMuch.Recompute()
	if !original.DriftedBeyondTolerance(tooMuch, inr(100)) {
		t.Error("a 10.00 drift beyond a 1.00 tolerance must require reapproval")
	}
}
