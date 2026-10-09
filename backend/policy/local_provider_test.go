package policy

import (
	"context"
	"testing"
)

func TestEvaluatePurchaseIntent_Allow(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "zepto",
		Category:         "groceries",
		AmountMinorUnits: 40000, // ₹400, under ₹1,000 approval threshold
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
		ShippingProfile:  "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Allow {
		t.Errorf("expected ALLOW, got %s (reasons: %v)", dec.Decision, dec.ReasonCodes)
	}
}

func TestEvaluatePurchaseIntent_RequiresApprovalAtThreshold(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "zepto",
		AmountMinorUnits: 100000, // exactly ₹1,000 threshold
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
		ShippingProfile:  "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != RequireApproval {
		t.Errorf("expected REQUIRE_APPROVAL, got %s", dec.Decision)
	}
	if dec.ApprovalRequirement == nil {
		t.Error("expected ApprovalRequirement to be set")
	}
}

func TestEvaluatePurchaseIntent_DeniesOverPerTransactionLimit(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "zepto",
		AmountMinorUnits: 200001, // ₹2,000.01, over the ₹2,000 per-tx limit
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
		ShippingProfile:  "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY, got %s", dec.Decision)
	}
}

func TestEvaluatePurchaseIntent_DeniesOverDailyLimitEvenIfPerTxOK(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:             "zepto",
		AmountMinorUnits:     50000,  // ₹500, well under per-tx limit
		SpendTodayMinorUnits: 480000, // already spent ₹4,800 today; +500 breaches ₹5,000
		Currency:             "INR",
		PaymentProfile:       "payment:personal",
		ShippingProfile:      "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY (daily limit), got %s: %v", dec.Decision, dec.ReasonCodes)
	}
}

func TestEvaluatePurchaseIntent_DeniesBlockedCategory(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "amazon",
		Category:         "gift_cards",
		AmountMinorUnits: 10000,
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
		ShippingProfile:  "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for blocked category, got %s", dec.Decision)
	}
	found := false
	for _, r := range dec.ReasonCodes {
		if r == "CATEGORY_BLOCKED" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CATEGORY_BLOCKED reason code, got %v", dec.ReasonCodes)
	}
}

func TestEvaluatePurchaseIntent_DeniesDisallowedPaymentProfile(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "zepto",
		AmountMinorUnits: 10000,
		Currency:         "INR",
		PaymentProfile:   "payment:travel", // not in AllowedPaymentProfiles
		ShippingProfile:  "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for disallowed payment profile, got %s", dec.Decision)
	}
}

func TestEvaluatePurchaseIntent_InternationalRequiresApproval(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "some-intl-merchant",
		AmountMinorUnits: 10000,
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
		ShippingProfile:  "shipping:home",
		International:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != RequireApproval {
		t.Errorf("expected REQUIRE_APPROVAL for international merchant, got %s", dec.Decision)
	}
}

func TestEvaluatePurchaseIntent_DenyBeatsRequireApproval(t *testing.T) {
	// Amount is over the per-transaction limit (DENY) AND international
	// (REQUIRE_APPROVAL) at once — DENY must win.
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant:         "some-intl-merchant",
		AmountMinorUnits: 900000,
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
		ShippingProfile:  "shipping:home",
		International:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY to win over REQUIRE_APPROVAL, got %s", dec.Decision)
	}
}

func TestEvaluateMerchant_Blocklist(t *testing.T) {
	rules := DefaultRules()
	rules.BlockedMerchants = []string{"shady-merchant"}
	p := NewLocalProvider(rules)

	dec, err := p.EvaluateMerchant(context.Background(), "shady-merchant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for blocked merchant, got %s", dec.Decision)
	}
}

func TestEvaluateMerchant_Allowlist(t *testing.T) {
	rules := DefaultRules()
	rules.AllowedMerchants = []string{"zepto", "blinkit"}
	p := NewLocalProvider(rules)

	if dec, _ := p.EvaluateMerchant(context.Background(), "zepto"); dec.Decision != Allow {
		t.Errorf("expected ALLOW for allowlisted merchant, got %s", dec.Decision)
	}
	if dec, _ := p.EvaluateMerchant(context.Background(), "amazon"); dec.Decision != Deny {
		t.Errorf("expected DENY for non-allowlisted merchant, got %s", dec.Decision)
	}
}

func TestEvaluatePayment_NeverReturnsRequireApproval_EvenAboveThreshold(t *testing.T) {
	// Regression test: EvaluatePayment runs at execution time, AFTER a
	// human has already approved a REQUIRE_APPROVAL purchase. If it
	// re-applied the approval-threshold rule, every over-threshold purchase
	// would loop forever between APPROVED and REAPPROVAL_REQUIRED and never
	// actually execute — see internal/app's
	// TestOrchestration_RequiresApproval_CrossUserRejected, which caught
	// exactly this before evaluateHardAmountCaps existed.
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePayment(context.Background(), Input{
		AmountMinorUnits: 150000, // ₹1,500 — above the ₹1,000 approval threshold, below the ₹2,000 cap
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Allow {
		t.Errorf("expected ALLOW (a human already approved this amount), got %s: %v", dec.Decision, dec.ReasonCodes)
	}
}

func TestEvaluatePayment_StillDeniesHardCapBreach(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePayment(context.Background(), Input{
		AmountMinorUnits: 250000, // ₹2,500 — over the ₹2,000 per-transaction cap, no approval can fix this
		Currency:         "INR",
		PaymentProfile:   "payment:personal",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for a hard per-transaction cap breach, got %s", dec.Decision)
	}
}

func TestEvaluatePayment_StillDeniesDailyCapBreach(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePayment(context.Background(), Input{
		AmountMinorUnits:     50000,
		SpendTodayMinorUnits: 480000, // already ₹4,800 today; +500 breaches the ₹5,000 daily cap
		Currency:             "INR",
		PaymentProfile:       "payment:personal",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for a daily cap breach, got %s", dec.Decision)
	}
}

func TestEvaluatePayment_StillDeniesDisallowedPaymentProfile(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	dec, err := p.EvaluatePayment(context.Background(), Input{
		AmountMinorUnits: 10000,
		Currency:         "INR",
		PaymentProfile:   "payment:travel",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for a disallowed payment profile, got %s", dec.Decision)
	}
}

func TestVersion_IsStamped(t *testing.T) {
	p := NewLocalProvider(DefaultRules())
	if p.Version() == "" {
		t.Error("expected non-empty policy version for audit trails")
	}
}

func TestEvaluatePurchaseIntent_CryptoRail_Allow(t *testing.T) {
	rules := DefaultRules()
	rules.MaxCryptoTxUSDC = "20.00"
	rules.CryptoConfirmThresholdUSDC = "10.00"
	p := NewLocalProvider(rules)

	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "5.00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Allow {
		t.Errorf("expected ALLOW, got %s (%v)", dec.Decision, dec.ReasonCodes)
	}
}

func TestEvaluatePurchaseIntent_CryptoRail_DeniesBlockedRecipient(t *testing.T) {
	rules := DefaultRules()
	rules.BlockedCryptoRecipients = []string{"0xbad"}
	p := NewLocalProvider(rules)

	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xbad", AmountUSDC: "1.00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for blocked recipient, got %s", dec.Decision)
	}
}

func TestEvaluatePurchaseIntent_CryptoRail_DeniesNonAllowlistedRecipient(t *testing.T) {
	rules := DefaultRules()
	rules.AllowedCryptoRecipients = []string{"0xgood"}
	p := NewLocalProvider(rules)

	if dec, _ := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xgood", AmountUSDC: "1.00"}); dec.Decision != Allow {
		t.Errorf("expected ALLOW for allowlisted recipient, got %s", dec.Decision)
	}
	if dec, _ := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xother", AmountUSDC: "1.00"}); dec.Decision != Deny {
		t.Errorf("expected DENY for non-allowlisted recipient, got %s", dec.Decision)
	}
}

func TestEvaluatePurchaseIntent_CryptoRail_DeniesOverMaxTx(t *testing.T) {
	rules := DefaultRules()
	rules.MaxCryptoTxUSDC = "20.00"
	p := NewLocalProvider(rules)

	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "20.01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY over max crypto tx, got %s", dec.Decision)
	}

	atCap, err := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "20.00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atCap.Decision == Deny {
		t.Errorf("expected exactly-at-cap to be allowed (max is a ceiling, not exclusive), got %s", atCap.Decision)
	}
}

func TestEvaluatePurchaseIntent_CryptoRail_RequiresApprovalAtConfirmThreshold(t *testing.T) {
	rules := DefaultRules()
	rules.CryptoConfirmThresholdUSDC = "10.00"
	p := NewLocalProvider(rules)

	below, err := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "5.00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if below.Decision != Allow {
		t.Errorf("expected ALLOW below confirm threshold, got %s", below.Decision)
	}

	at, err := p.EvaluatePurchaseIntent(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "10.00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if at.Decision != RequireApproval {
		t.Errorf("expected REQUIRE_APPROVAL at confirm threshold, got %s", at.Decision)
	}
	if at.ApprovalRequirement == nil {
		t.Error("expected ApprovalRequirement to be set")
	}
}

// TestEvaluatePayment_CryptoRail_NeverRequiresApproval mirrors
// TestEvaluatePayment_NeverReturnsRequireApproval_EvenAboveThreshold for the
// crypto rail: the execute-time re-check must not re-demand a confirmation
// a human already granted.
func TestEvaluatePayment_CryptoRail_NeverRequiresApproval(t *testing.T) {
	rules := DefaultRules()
	rules.CryptoConfirmThresholdUSDC = "10.00"
	rules.MaxCryptoTxUSDC = "100.00"
	p := NewLocalProvider(rules)

	dec, err := p.EvaluatePayment(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "50.00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Allow {
		t.Errorf("expected ALLOW (a human already approved this payment), got %s", dec.Decision)
	}
}

func TestEvaluatePayment_CryptoRail_StillDeniesOverMaxTx(t *testing.T) {
	rules := DefaultRules()
	rules.MaxCryptoTxUSDC = "20.00"
	p := NewLocalProvider(rules)

	dec, err := p.EvaluatePayment(context.Background(), Input{CryptoRecipient: "0xabc", AmountUSDC: "20.01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY for a hard crypto per-transaction cap breach, got %s", dec.Decision)
	}
}

// TestEvaluatePurchaseIntent_PartialCryptoFields_TreatedAsOrdinary guards
// against a half-populated Input (e.g. AmountUSDC set but CryptoRecipient
// forgotten) silently skipping every INR rule instead of being evaluated as
// an ordinary purchase — incomplete crypto context is not "close enough" to
// route specially.
func TestEvaluatePurchaseIntent_PartialCryptoFields_TreatedAsOrdinary(t *testing.T) {
	rules := DefaultRules()
	rules.BlockedMerchants = []string{"mock"}
	p := NewLocalProvider(rules)

	dec, err := p.EvaluatePurchaseIntent(context.Background(), Input{
		Merchant: "mock", AmountUSDC: "5.00", AmountMinorUnits: 10000, Currency: "INR",
		PaymentProfile: "payment:personal", ShippingProfile: "shipping:home",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Decision != Deny {
		t.Errorf("expected DENY via the ordinary merchant blocklist, got %s: %v", dec.Decision, dec.ReasonCodes)
	}
}

func TestParseUSDCMicros(t *testing.T) {
	cases := map[string]int64{
		"10.00":      10_000_000,
		"10":         10_000_000,
		"0.50":       500_000,
		"10.5":       10_500_000,
		"0.000001":   1,
		"100.123456": 100_123_456,
	}
	for input, want := range cases {
		got, err := parseUSDCMicros(input)
		if err != nil {
			t.Errorf("parseUSDCMicros(%q) unexpected error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseUSDCMicros(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestUsdcAtOrAbove(t *testing.T) {
	if exceeds, err := usdcAtOrAbove("9.99", "10.00"); err != nil || exceeds {
		t.Errorf("expected 9.99 to NOT be at/above 10.00, got exceeds=%v err=%v", exceeds, err)
	}
	if exceeds, err := usdcAtOrAbove("10.00", "10.00"); err != nil || !exceeds {
		t.Errorf("expected 10.00 to be at/above 10.00, got exceeds=%v err=%v", exceeds, err)
	}
	if exceeds, err := usdcAtOrAbove("10.01", "10.00"); err != nil || !exceeds {
		t.Errorf("expected 10.01 to be at/above 10.00, got exceeds=%v err=%v", exceeds, err)
	}
}

func TestUsdcAbove(t *testing.T) {
	if exceeds, err := usdcAbove("10.00", "10.00"); err != nil || exceeds {
		t.Errorf("expected 10.00 to NOT be strictly above 10.00, got exceeds=%v err=%v", exceeds, err)
	}
	if exceeds, err := usdcAbove("10.01", "10.00"); err != nil || !exceeds {
		t.Errorf("expected 10.01 to be strictly above 10.00, got exceeds=%v err=%v", exceeds, err)
	}
}
