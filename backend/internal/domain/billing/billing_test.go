package billing

import "testing"

func TestEffectivePlan(t *testing.T) {
	if EffectivePlan(nil) != PlanDeveloper {
		t.Error("no subscription must mean the free plan")
	}
	cases := map[Status]Plan{
		StatusCreated:       PlanDeveloper, // checkout never finished
		StatusAuthenticated: PlanGrowth,
		StatusActive:        PlanGrowth,
		StatusPending:       PlanGrowth, // keep access while a renewal retries
		StatusHalted:        PlanDeveloper,
		StatusCancelled:     PlanDeveloper,
		StatusCompleted:     PlanDeveloper,
	}
	for st, want := range cases {
		if got := EffectivePlan(&Subscription{Plan: PlanGrowth, Status: st}); got != want {
			t.Errorf("%s: got %s, want %s", st, got, want)
		}
	}
}

func TestEntitlementsMatchPricingPage(t *testing.T) {
	if e := Entitlements[PlanDeveloper]; e.IncludedExecutions != 100 || !e.HardLimit {
		t.Errorf("developer: %+v", e)
	}
	if e := Entitlements[PlanGrowth]; e.IncludedExecutions != 5000 || e.HardLimit {
		t.Errorf("growth: %+v", e)
	}
}
