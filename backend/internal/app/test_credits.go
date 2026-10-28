package app

import (
	"context"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// DenyTestCreditsExhausted: the account has used its Testnet credits.
const DenyTestCreditsExhausted = "TEST_CREDITS_EXHAUSTED"

// Default Testnet credits: what every account starts with.
const (
	DefaultTestCreditUSDCMinor   int64 = 10_000_000    // 10 USDC
	DefaultTestCreditSOLLamports int64 = 5_000_000_000 // 5 SOL
)

// TestCreditSpend reads what an account has used of its credits on the given
// rails: holds that may still become payments, plus payments that delivered.
// A payment the provider took but whose answer was unusable (NOT_FULFILLED)
// is not counted, so its credits come back on their own.
type TestCreditSpend interface {
	TestCreditSpend(ctx context.Context, userID string, rails []string) (int64, error)
}

// CreditBalance is an account's Testnet credits.
type CreditBalance struct {
	Network          string `json:"network"`
	USDCGrantedMinor int64  `json:"usdc_granted_minor"`
	USDCUsedMinor    int64  `json:"usdc_used_minor"`
	USDCLeftMinor    int64  `json:"usdc_left_minor"`
	SOLLamports      int64  `json:"sol_lamports"`
}

// TestCredits is every account's practice money on Testnet. Payments there
// are real devnet transactions from Algebra's own test wallet; the credits are
// what one account may have that wallet pay for it. There is no second
// ledger to drift from the payments: the balance is read from the payment
// records themselves (TestCreditSpend), so a payment held, settled or given
// back moves it exactly once.
//
// It is a PassFunder that moves no money. Fund refuses a payment the credits
// can't cover; the reservation it is asked about is already recorded, so two
// payments at once both see each other and neither overspends (at worst both
// are refused and one is asked again).
type TestCredits struct {
	rails    []string
	railSet  map[string]bool
	network  string
	passes   PassOwners
	spend    TestCreditSpend
	usdc     int64
	lamports int64
}

// NewTestCredits gives each account usdcMinor of USDC (and lamports of SOL,
// shown with the balance) to spend on the named rails.
func NewTestCredits(rails []string, network string, passes PassOwners, spend TestCreditSpend, usdcMinor, lamports int64) *TestCredits {
	c := &TestCredits{rails: rails, railSet: map[string]bool{}, network: network, passes: passes, spend: spend, usdc: usdcMinor, lamports: lamports}
	for _, r := range rails {
		c.railSet[r] = true
	}
	return c
}

// Balance is what the account has left.
func (c *TestCredits) Balance(ctx context.Context, userID string) (CreditBalance, error) {
	used, err := c.spend.TestCreditSpend(ctx, userID, c.rails)
	if err != nil {
		return CreditBalance{}, fmt.Errorf("test credits: reading what was used: %w", err)
	}
	return CreditBalance{
		Network: c.network, USDCGrantedMinor: c.usdc, USDCUsedMinor: used,
		USDCLeftMinor: max(c.usdc-used, 0), SOLLamports: c.lamports,
	}, nil
}

// Check never refuses: freezing and revoking a pass are on the pass itself.
func (c *TestCredits) Check(context.Context, string) error { return nil }

// Funding says the payment is not from a vault.
func (c *TestCredits) Funding(context.Context, string, string) (bool, error) { return false, nil }

// Fund refuses a payment the account's credits can't cover. The reservation
// rv is already counted at its hold; the payment is the amount asked, which
// is never more than the hold.
func (c *TestCredits) Fund(ctx context.Context, passID string, rv *econ.Reservation, amountMinor int64) (*OnchainPull, error) {
	if c == nil || !c.railSet[rv.Rail] || amountMinor <= 0 {
		return nil, nil
	}
	pass, err := c.passes.Get(ctx, passID)
	if err != nil {
		return nil, fmt.Errorf("test credits: reading the pass: %w", err)
	}
	if pass.UserID == "" {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyTestCreditsExhausted}}
	}
	used, err := c.spend.TestCreditSpend(ctx, pass.UserID, c.rails)
	if err != nil {
		return nil, fmt.Errorf("test credits: reading what was used: %w", err)
	}
	if used-rv.HoldMinor+amountMinor > c.usdc {
		return nil, &AuthorityDenied{ReasonCodes: []string{DenyTestCreditsExhausted}}
	}
	return nil, nil
}
