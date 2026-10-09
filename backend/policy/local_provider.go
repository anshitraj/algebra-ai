package policy

import (
	"context"
	"fmt"
	"time"
)

// LocalProvider is Algebra's own deterministic rule-based PolicyProvider. It
// is real, not a mock — it makes actual ALLOW/DENY/REQUIRE_APPROVAL
// decisions from Rules — and is the default provider until an external
// engine is wired up.
type LocalProvider struct {
	rules Rules
}

// NewLocalProvider constructs a LocalProvider over the given ruleset.
func NewLocalProvider(rules Rules) *LocalProvider {
	return &LocalProvider{rules: rules}
}

func (p *LocalProvider) Version() string { return p.rules.Version }

func (p *LocalProvider) decision(d Decision, reason string) *PolicyDecision {
	pd := &PolicyDecision{
		Decision:      d,
		ReasonCodes:   []string{reason},
		PolicyVersion: p.rules.Version,
		EvaluatedAt:   time.Now().UTC(),
	}
	if d == RequireApproval {
		pd.ApprovalRequirement = &ApprovalRequirement{Reason: reason}
	}
	return pd
}

func (p *LocalProvider) EvaluateMerchant(_ context.Context, merchant string) (*PolicyDecision, error) {
	if contains(p.rules.BlockedMerchants, merchant) {
		return p.decision(Deny, "MERCHANT_BLOCKED"), nil
	}
	if len(p.rules.AllowedMerchants) > 0 && !contains(p.rules.AllowedMerchants, merchant) {
		return p.decision(Deny, "MERCHANT_NOT_ALLOWLISTED"), nil
	}
	return p.decision(Allow, "MERCHANT_OK"), nil
}

func (p *LocalProvider) EvaluateCategory(_ context.Context, category string) (*PolicyDecision, error) {
	if category != "" && contains(p.rules.BlockedCategories, category) {
		return p.decision(Deny, "CATEGORY_BLOCKED"), nil
	}
	return p.decision(Allow, "CATEGORY_OK"), nil
}

func (p *LocalProvider) EvaluateAmount(_ context.Context, amountMinorUnits int64, currency string, spendTodayMinorUnits int64) (*PolicyDecision, error) {
	if p.rules.Currency != "" && currency != p.rules.Currency {
		return p.decision(Deny, "CURRENCY_NOT_SUPPORTED"), nil
	}
	if p.rules.MaxPerTransactionMinorUnits > 0 && amountMinorUnits > p.rules.MaxPerTransactionMinorUnits {
		return p.decision(Deny, "AMOUNT_EXCEEDS_PER_TRANSACTION_LIMIT"), nil
	}
	if p.rules.MaxPerDayMinorUnits > 0 && spendTodayMinorUnits+amountMinorUnits > p.rules.MaxPerDayMinorUnits {
		return p.decision(Deny, "AMOUNT_EXCEEDS_DAILY_LIMIT"), nil
	}
	if p.rules.ApprovalThresholdMinorUnits > 0 && amountMinorUnits >= p.rules.ApprovalThresholdMinorUnits {
		return p.decision(RequireApproval, "AMOUNT_AT_OR_ABOVE_APPROVAL_THRESHOLD"), nil
	}
	return p.decision(Allow, "AMOUNT_OK"), nil
}

// evaluateHardAmountCaps checks only the caps that DENY regardless of
// approval (per-transaction limit, daily limit, currency) — never the
// approval-threshold rule. It deliberately does NOT reuse EvaluateAmount:
// that method's RequireApproval result means "a human must click approve,"
// which is a question about what happens BEFORE an Approval exists. Once an
// Approval exists (this is what EvaluatePayment, called from
// OrderService.Execute, guards), re-asking "does this amount need approval"
// would re-fire RequireApproval for the exact amount a human already
// approved, on every single execution attempt — an unrecoverable loop, not
// a safety check. Hard caps, by contrast, are correct to re-check every
// time: no human approval can override a per-transaction/daily limit.
func (p *LocalProvider) evaluateHardAmountCaps(amountMinorUnits int64, currency string, spendTodayMinorUnits int64) *PolicyDecision {
	if p.rules.Currency != "" && currency != p.rules.Currency {
		return p.decision(Deny, "CURRENCY_NOT_SUPPORTED")
	}
	if p.rules.MaxPerTransactionMinorUnits > 0 && amountMinorUnits > p.rules.MaxPerTransactionMinorUnits {
		return p.decision(Deny, "AMOUNT_EXCEEDS_PER_TRANSACTION_LIMIT")
	}
	if p.rules.MaxPerDayMinorUnits > 0 && spendTodayMinorUnits+amountMinorUnits > p.rules.MaxPerDayMinorUnits {
		return p.decision(Deny, "AMOUNT_EXCEEDS_DAILY_LIMIT")
	}
	return p.decision(Allow, "AMOUNT_WITHIN_HARD_LIMITS")
}

func (p *LocalProvider) EvaluatePaymentSource(_ context.Context, paymentProfile string) (*PolicyDecision, error) {
	if len(p.rules.AllowedPaymentProfiles) > 0 && !contains(p.rules.AllowedPaymentProfiles, paymentProfile) {
		return p.decision(Deny, "PAYMENT_PROFILE_NOT_ALLOWED"), nil
	}
	return p.decision(Allow, "PAYMENT_PROFILE_OK"), nil
}

func (p *LocalProvider) evaluateShipping(shippingProfile string) *PolicyDecision {
	if len(p.rules.AllowedShippingProfiles) > 0 && !contains(p.rules.AllowedShippingProfiles, shippingProfile) {
		return p.decision(Deny, "SHIPPING_PROFILE_NOT_ALLOWED")
	}
	return p.decision(Allow, "SHIPPING_PROFILE_OK")
}

func (p *LocalProvider) evaluateInternational(international bool) *PolicyDecision {
	if international && p.rules.InternationalRequiresApproval {
		return p.decision(RequireApproval, "INTERNATIONAL_MERCHANT_REQUIRES_APPROVAL")
	}
	return p.decision(Allow, "INTERNATIONAL_OK")
}

// isCryptoRail reports whether in describes a crypto-rail payment
// (x402 / on-chain transfer) rather than an ordinary merchant/INR purchase —
// see Input's doc comment. Both fields must be set: a half-populated Input
// is treated as ordinary, not "close enough" to crypto-shaped.
func isCryptoRail(in Input) bool {
	return in.CryptoRecipient != "" && in.AmountUSDC != ""
}

// evaluateCryptoRecipient mirrors EvaluateMerchant for a wallet-address-or-
// URL recipient — ported from the recipient allow/block-list logic in
// third_party/omniclaw/src/omniclaw/guards/recipient.py (MIT-licensed;
// simplified to exact address/URL matching, no regex patterns, since
// nothing in Algebra generates those).
func (p *LocalProvider) evaluateCryptoRecipient(recipient string) *PolicyDecision {
	if contains(p.rules.BlockedCryptoRecipients, recipient) {
		return p.decision(Deny, "CRYPTO_RECIPIENT_BLOCKED")
	}
	if len(p.rules.AllowedCryptoRecipients) > 0 && !contains(p.rules.AllowedCryptoRecipients, recipient) {
		return p.decision(Deny, "CRYPTO_RECIPIENT_NOT_ALLOWLISTED")
	}
	return p.decision(Allow, "CRYPTO_RECIPIENT_OK")
}

// evaluateCryptoAmount enforces the crypto-rail per-transaction cap — ported
// from third_party/omniclaw/src/omniclaw/guards/single_tx.py's max_amount
// check (MIT-licensed).
func (p *LocalProvider) evaluateCryptoAmount(amountUSDC string) (*PolicyDecision, error) {
	if p.rules.MaxCryptoTxUSDC != "" {
		exceeds, err := usdcAbove(amountUSDC, p.rules.MaxCryptoTxUSDC)
		if err != nil {
			return nil, fmt.Errorf("policy: evaluating crypto amount: %w", err)
		}
		if exceeds {
			return p.decision(Deny, "CRYPTO_AMOUNT_EXCEEDS_PER_TRANSACTION_LIMIT"), nil
		}
	}
	return p.decision(Allow, "CRYPTO_AMOUNT_OK"), nil
}

// evaluateCryptoConfirmThreshold requires approval at or above the
// configured threshold — ported from
// third_party/omniclaw/src/omniclaw/guards/confirm.py's threshold check
// (MIT-licensed): "amount >= threshold" needs confirmation.
func (p *LocalProvider) evaluateCryptoConfirmThreshold(amountUSDC string) (*PolicyDecision, error) {
	if p.rules.CryptoConfirmThresholdUSDC != "" {
		exceeds, err := usdcAtOrAbove(amountUSDC, p.rules.CryptoConfirmThresholdUSDC)
		if err != nil {
			return nil, fmt.Errorf("policy: evaluating crypto confirm threshold: %w", err)
		}
		if exceeds {
			return p.decision(RequireApproval, "CRYPTO_AMOUNT_AT_OR_ABOVE_CONFIRM_THRESHOLD"), nil
		}
	}
	return p.decision(Allow, "CRYPTO_CONFIRM_THRESHOLD_OK"), nil
}

// evaluateCryptoPurchaseIntent is the crypto-rail counterpart to the
// merchant/category/amount/payment-source checks EvaluatePurchaseIntent
// runs for an INR purchase: recipient allow/block-list, hard per-tx cap,
// then confirm threshold.
func (p *LocalProvider) evaluateCryptoPurchaseIntent(in Input) (*PolicyDecision, error) {
	recipientDec := p.evaluateCryptoRecipient(in.CryptoRecipient)
	amountDec, err := p.evaluateCryptoAmount(in.AmountUSDC)
	if err != nil {
		return nil, err
	}
	confirmDec, err := p.evaluateCryptoConfirmThreshold(in.AmountUSDC)
	if err != nil {
		return nil, err
	}
	return merge(recipientDec, amountDec, confirmDec), nil
}

// EvaluatePurchaseIntent runs every sub-check and merges them: any DENY
// wins outright, otherwise any REQUIRE_APPROVAL wins, otherwise ALLOW.
func (p *LocalProvider) EvaluatePurchaseIntent(ctx context.Context, in Input) (*PolicyDecision, error) {
	if isCryptoRail(in) {
		return p.evaluateCryptoPurchaseIntent(in)
	}

	merchantDec, err := p.EvaluateMerchant(ctx, in.Merchant)
	if err != nil {
		return nil, err
	}
	categoryDec, err := p.EvaluateCategory(ctx, in.Category)
	if err != nil {
		return nil, err
	}
	amountDec, err := p.EvaluateAmount(ctx, in.AmountMinorUnits, in.Currency, in.SpendTodayMinorUnits)
	if err != nil {
		return nil, err
	}
	paymentDec, err := p.EvaluatePaymentSource(ctx, in.PaymentProfile)
	if err != nil {
		return nil, err
	}
	shippingDec := p.evaluateShipping(in.ShippingProfile)
	intlDec := p.evaluateInternational(in.International)

	return merge(merchantDec, categoryDec, amountDec, paymentDec, shippingDec, intlDec), nil
}

// EvaluatePayment re-checks hard amount caps + payment source — run
// immediately before authorization to catch a payment-source switch or a
// daily-limit breach (e.g. another purchase already landed today) after the
// intent was originally cleared. It intentionally never returns
// RequireApproval — see evaluateHardAmountCaps. For a crypto-rail Input,
// the equivalent is recipient + hard per-tx cap, never the confirm
// threshold — the same "don't re-demand an approval already granted"
// reasoning applies.
func (p *LocalProvider) EvaluatePayment(ctx context.Context, in Input) (*PolicyDecision, error) {
	if isCryptoRail(in) {
		recipientDec := p.evaluateCryptoRecipient(in.CryptoRecipient)
		amountDec, err := p.evaluateCryptoAmount(in.AmountUSDC)
		if err != nil {
			return nil, err
		}
		return merge(recipientDec, amountDec), nil
	}

	amountDec := p.evaluateHardAmountCaps(in.AmountMinorUnits, in.Currency, in.SpendTodayMinorUnits)
	paymentDec, err := p.EvaluatePaymentSource(ctx, in.PaymentProfile)
	if err != nil {
		return nil, err
	}
	return merge(amountDec, paymentDec), nil
}
