package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/account"
)

func TestOnboarding_SavesProviderInterestsWithoutChangingPermissions(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	signedIn, err := h.svc.SignUp(ctx, "Ada", "onboarding@example.com", "longpassword", meta)
	if err != nil {
		t.Fatal(err)
	}
	profiles := NewCommerceProfileService(newFakeCommerceProfileStore(), h.agents)
	onboarding := NewOnboardingService(h.svc, profiles, nil)
	before, err := h.agents.Get(ctx, signedIn.Session.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	wantPermissions := append(before.Permissions[:0:0], before.Permissions...)
	if err := onboarding.Complete(ctx, signedIn.Session, OnboardingAnswers{
		ProviderCategories: []string{" AI_ML ", "search", "ai_ml", "unknown"},
		UseCases:           []string{"digital_services"},
		Guardrails:         account.Guardrails{Currency: "INR", MaxPerDayMinorUnits: 500_000, MaxPerPurchaseMinorUnits: 500_000, ApprovalThresholdMinorUnits: 100_000},
	}); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.GetOrEmpty(ctx, signedIn.Session.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if got := profile.Preferences["shopping"]["provider_categories"]; !reflect.DeepEqual(got, []string{"ai_ml", "search"}) {
		t.Fatalf("saved interests = %v", got)
	}
	user, err := h.store.GetUser(ctx, signedIn.User.ID)
	if err != nil || user.OnboardedAt == nil {
		t.Fatalf("onboarding was not completed: %v", err)
	}
	after, err := h.agents.Get(ctx, signedIn.Session.AgentID)
	if err != nil || !reflect.DeepEqual(after.Permissions, wantPermissions) {
		t.Fatalf("interests changed agent permissions: %v", err)
	}
}
