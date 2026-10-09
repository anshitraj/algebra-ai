package app

import (
	"context"
	"fmt"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/privacy"
)

// DemoService starts demo accounts: one click from the sign-in page, no
// signup, straight into a fully set-up console. The account goes through
// the same onboarding a real one does — only the answers are fixed — so the
// demo exercises the real guardrails, profile and address code, not a
// parallel fake.
type DemoService struct {
	accounts   *AccountService
	onboarding *OnboardingService
}

func NewDemoService(accounts *AccountService, onboarding *OnboardingService) *DemoService {
	return &DemoService{accounts: accounts, onboarding: onboarding}
}

// DemoGuardrails are chosen so a demo shows every policy outcome within a
// few purchases: small buys go straight through, anything from ₹1,000 asks
// for approval, and a big-ticket item hits the per-purchase cap.
var DemoGuardrails = account.Guardrails{
	Currency:                      "INR",
	ApprovalThresholdMinorUnits:   100000,  // ₹1,000
	MaxPerPurchaseMinorUnits:      5000000, // ₹50,000
	MaxPerDayMinorUnits:           10000000,
	BlockedCategories:             []string{"alcohol", "tobacco", "gift_cards"},
	InternationalRequiresApproval: true,
}

// DemoAddress is a made-up address in a real neighbourhood, so the order
// map has somewhere to point. Nothing is ever delivered to it.
var DemoAddress = privacy.ShippingProfile{
	RecipientName: "Demo Shopper",
	Line1:         "12, 100 Feet Road",
	Line2:         "HAL 2nd Stage, Indiranagar",
	City:          "Bengaluru",
	State:         "Karnataka",
	PostalCode:    "560038",
	Country:       "IN",
	Phone:         "+91 90000 00000",
}

// Start creates and signs in a new demo account, onboarded and ready to
// shop.
func (s *DemoService) Start(ctx context.Context, meta account.ClientMeta) (*SignInResult, error) {
	res, err := s.accounts.StartDemo(ctx, meta)
	if err != nil {
		return nil, err
	}
	address := DemoAddress
	answers := OnboardingAnswers{
		Name:       "Demo shopper",
		UseCases:   []string{"electronics", "fashion", "groceries", "home", "beauty"},
		Priority:   "best_value",
		Household:  "couple",
		Guardrails: DemoGuardrails,
		Shipping:   &address,
	}
	if err := s.onboarding.Complete(ctx, res.Session, answers); err != nil {
		_ = s.accounts.SignOut(ctx, res.Session)
		return nil, fmt.Errorf("app: setting up demo account: %w", err)
	}
	u, err := s.accounts.User(ctx, res.User.ID)
	if err != nil {
		return nil, err
	}
	res.User = u
	return res, nil
}
