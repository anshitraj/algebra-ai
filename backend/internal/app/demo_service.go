package app

import (
	"context"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// DemoService starts demo accounts: one click from the sign-in page, no
// signup, straight into a fully set-up console. The account goes through
// the same onboarding a real one does — only the answers are fixed — so the
// demo exercises the real guardrails, profile and address code, not a
// parallel fake.
type DemoService struct {
	accounts   *AccountService
	onboarding *OnboardingService
	passes     *SpendPassService
}

// SetPasses gives every new demo account a Spend Pass for the sandbox sellers.
func (s *DemoService) SetPasses(p *SpendPassService) { s.passes = p }

// DemoPassBudgetMinor and DemoPassPerCallMinor bound a demo account's pass:
// enough for every sandbox seller several times over, in simulated USDC.
const (
	DemoPassBudgetMinor  = 1_000_000 // 1 USDC
	DemoPassPerCallMinor = 50_000    // 0.05 USDC
)

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
	if s.passes != nil {
		// Ready to pay the sandbox sellers at once. A demo account pays only in
		// the sandbox (EconomicService.restrictDemo), so this is simulated USDC.
		perCall := int64(DemoPassPerCallMinor)
		if _, err := s.passes.Create(ctx, res.User.ID, spendpass.Pass{
			Label: "Demo agent (simulated USDC)", AgentKind: spendpass.AgentCustom, Currency: "USDC",
			BudgetMinorUnits: DemoPassBudgetMinor, BudgetPeriod: spendpass.PeriodTotal, MaxPerPurchaseMinorUnits: &perCall,
			Controls:  spendpass.Controls{NewProviders: spendpass.NewProvidersAllow},
			ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		}); err != nil {
			_ = s.accounts.SignOut(ctx, res.Session)
			return nil, fmt.Errorf("app: setting up the demo account's pass: %w", err)
		}
	}
	u, err := s.accounts.User(ctx, res.User.ID)
	if err != nil {
		return nil, err
	}
	res.User = u
	return res, nil
}
