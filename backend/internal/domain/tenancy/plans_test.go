package tenancy

import (
	"testing"
	"time"
)

func TestPlansMatchThePricingPage(t *testing.T) {
	want := []struct {
		id    string
		cents int
	}{{PlanFree, 0}, {PlanBuilder, 1900}, {PlanStartup, 5900}, {PlanBusiness, 19900}, {PlanEnterprise, Unlimited}}
	if len(Plans) != len(want) {
		t.Fatalf("plans: %d", len(Plans))
	}
	for i, w := range want {
		if Plans[i].ID != w.id || Plans[i].MonthlyUSDCents != w.cents {
			t.Errorf("plan %d: got %s at %d, want %s at %d", i, Plans[i].ID, Plans[i].MonthlyUSDCents, w.id, w.cents)
		}
	}
	if !PlanByID(TrialPlan).Trial || TrialLength != 7*24*time.Hour {
		t.Error("the trial is seven days of Startup")
	}
	if PlanByID(PlanFree).Projects != 1 {
		t.Error("Free includes one project")
	}
	if PlanByID("no-such-plan").ID != PlanFree {
		t.Error("an unknown plan must be the free plan, never more")
	}
}

func TestATrialGivesItsPlanOnlyWhileItRuns(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	ends := now.Add(TrialLength)
	o := Organization{Plan: PlanFree}
	if o.EffectivePlan(now).ID != PlanFree || o.TrialActive(now) || o.TrialUsed() {
		t.Fatal("no trial: the organization's own plan")
	}
	o.TrialPlan, o.TrialEndsAt = TrialPlan, &ends
	if o.EffectivePlan(now).ID != PlanStartup || !o.TrialActive(now) {
		t.Fatal("during the trial: Startup")
	}
	if o.EffectivePlan(ends.Add(-time.Second)).ID != PlanStartup {
		t.Fatal("the last second of the trial is still the trial")
	}
	for _, at := range []time.Time{ends, ends.Add(time.Hour)} {
		if o.EffectivePlan(at).ID != PlanFree || o.TrialActive(at) {
			t.Fatalf("at %s the trial is over: back to Free with nothing to undo", at)
		}
	}
	if !o.TrialUsed() {
		t.Fatal("a trial that ended was still used: there is one each")
	}
	// A paid plan above the trial's is never lowered by an old trial row.
	o.Plan = PlanBusiness
	if o.EffectivePlan(ends.Add(time.Hour)).ID != PlanBusiness {
		t.Fatal("after the trial: the organization's own plan")
	}
}

func TestAllows(t *testing.T) {
	if !Allows(1, 0) || Allows(1, 1) || Allows(3, 5) || !Allows(Unlimited, 1_000_000) {
		t.Error("Allows: a limit admits one more only below it; Unlimited always")
	}
}
