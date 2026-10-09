package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/privacy"
)

// ShippingStore is the write half of privacy.Resolver the onboarding flow
// needs. The address is encrypted inside StoreShipping; this service never
// holds it longer than the call.
type ShippingStore interface {
	StoreShipping(ctx context.Context, id, userID, alias string, profile privacy.ShippingProfile, now time.Time) error
}

// OnboardingAnswers is the whole first-run questionnaire — a handful of
// multiple-choice answers, each of which changes how the agent behaves.
// Nothing here is collected for its own sake.
type OnboardingAnswers struct {
	Name string `json:"name"`

	// UseCases: what the agent will shop for (account.KnownCategories).
	UseCases []string `json:"use_cases"`
	// Priority: how the agent picks between quotes.
	Priority string `json:"priority"`
	// Household: sizes default quantities for staples.
	Household string `json:"household"`
	// Dietary: filters food/grocery searches.
	Dietary []string `json:"dietary"`
	// PreferredMerchants: tried first, in this order.
	PreferredMerchants []string `json:"preferred_merchants"`

	Guardrails account.Guardrails `json:"guardrails"`

	// Shipping is optional — "add it later" is a valid answer.
	Shipping *privacy.ShippingProfile `json:"shipping,omitempty"`
}

var (
	validPriorities = []string{"lowest_price", "fastest_delivery", "trusted_brands", "best_value"}
	validHouseholds = []string{"solo", "couple", "family", "large"}
	validDietary    = []string{"vegetarian", "vegan", "eggetarian", "jain", "halal", "gluten_free", "no_restrictions"}
)

// DefaultShippingAlias / DefaultPaymentAlias are the aliases the console
// and the platform-default policy agree on.
const (
	DefaultShippingAlias = "shipping:home"
	DefaultPaymentAlias  = "payment:personal"
)

// OnboardingService turns the questionnaire into the three places those
// answers actually take effect: the user's guardrails (policy engine), their
// commerce profile (what the agent reads before asking questions), and
// optionally an encrypted home address.
type OnboardingService struct {
	accounts *AccountService
	profiles *CommerceProfileService
	shipping ShippingStore
	now      func() time.Time
}

func NewOnboardingService(accounts *AccountService, profiles *CommerceProfileService, shipping ShippingStore) *OnboardingService {
	return &OnboardingService{accounts: accounts, profiles: profiles, shipping: shipping, now: time.Now}
}

func pickAllowed(in []string, allowed []string) []string {
	out := []string{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if slices.Contains(allowed, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func oneOf(v string, allowed []string, def string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if slices.Contains(allowed, v) {
		return v
	}
	return def
}

// Complete applies the answers for the session's user. It writes through
// the session's own console agent for the commerce profile, so the same
// permission checks apply as when the agent itself learns a preference.
func (s *OnboardingService) Complete(ctx context.Context, sess *account.Session, in OnboardingAnswers) error {
	guardrails, err := s.accounts.SetGuardrails(ctx, sess.UserID, in.Guardrails)
	if err != nil {
		return fmt.Errorf("guardrails: %w", err)
	}

	if name := strings.TrimSpace(in.Name); name != "" {
		if _, err := s.accounts.UpdateName(ctx, sess.UserID, name); err != nil {
			return err
		}
	}

	if in.Shipping != nil {
		sh := *in.Shipping
		if sh.Country == "" {
			sh.Country = "IN"
		}
		if strings.TrimSpace(sh.RecipientName) == "" || strings.TrimSpace(sh.Line1) == "" ||
			strings.TrimSpace(sh.City) == "" || strings.TrimSpace(sh.PostalCode) == "" {
			return fmt.Errorf("address needs a name, street, city and PIN code — or skip it for now")
		}
		if err := s.shipping.StoreShipping(ctx, newID("profile"), sess.UserID, DefaultShippingAlias, sh, s.now()); err != nil {
			return fmt.Errorf("saving address: %w", err)
		}
	}

	useCases := pickAllowed(in.UseCases, account.KnownCategories)
	shopping := map[string]any{
		"shops_for":          useCases,
		"priority":           oneOf(in.Priority, validPriorities, "best_value"),
		"household":          oneOf(in.Household, validHouseholds, "solo"),
		"approval_threshold": guardrails.ApprovalThresholdMinorUnits,
		"daily_limit":        guardrails.MaxPerDayMinorUnits,
		"never_buy":          guardrails.BlockedCategories,
	}
	if merchants := cleanList(in.PreferredMerchants, 8); len(merchants) > 0 {
		shopping["preferred_merchants"] = merchants
	}
	if _, err := s.profiles.SetPreferences(ctx, sess.AgentID, "shopping", shopping); err != nil {
		return fmt.Errorf("saving preferences: %w", err)
	}
	if diet := pickAllowed(in.Dietary, validDietary); len(diet) > 0 {
		if _, err := s.profiles.SetPreferences(ctx, sess.AgentID, "food", map[string]any{"dietary": diet}); err != nil {
			return fmt.Errorf("saving preferences: %w", err)
		}
	}

	shippingAlias := ""
	if in.Shipping != nil {
		shippingAlias = DefaultShippingAlias
	}
	paymentAlias := DefaultPaymentAlias
	var shipPtr *string
	if shippingAlias != "" {
		shipPtr = &shippingAlias
	}
	if _, err := s.profiles.SetDefaultAliases(ctx, sess.AgentID, shipPtr, &paymentAlias); err != nil {
		return fmt.Errorf("saving defaults: %w", err)
	}

	return s.accounts.MarkOnboarded(ctx, sess.UserID)
}

func cleanList(in []string, max int) []string {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" && len(v) <= 64 && !slices.Contains(out, v) {
			out = append(out, v)
		}
		if len(out) == max {
			break
		}
	}
	return out
}
