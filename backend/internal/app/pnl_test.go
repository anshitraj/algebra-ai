package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/jobpass"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// payUnusable is a payment that settled but bought nothing usable.
func (rig *jobRig) payUnusable(t *testing.T, job string, quote int64) {
	t.Helper()
	ctx := context.Background()
	id := rig.newIntent(t)
	r, err := rig.reserve(job, id, quote)
	if err != nil {
		t.Fatal(err)
	}
	if r, err = rig.svc.Begin(ctx, "agent_1", id, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.AuthorizePayment(ctx, "agent_1", id, r.ID, PaymentRequest{}); err != nil {
		t.Fatal(err)
	}
	rig.rail.land(r, quote)
	if _, err := rig.svc.Complete(ctx, "agent_1", id, r.ID, CompletionReport{Outcome: econ.OutcomeSettled, Fulfillment: econ.FulfillmentNotFulfilled}); err != nil {
		t.Fatal(err)
	}
}

func TestPnL_ATaskIsItsValueLessWhatItPaid(t *testing.T) {
	ctx := context.Background()
	rig := newJobRig(t, 1_000_000)
	rig.jobs.SetPnL(rig.store)
	value := int64(10_000)
	j, err := rig.jobs.Open(ctx, OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "Research", BudgetMinor: 50_000, ValueMinor: &value}})
	if err != nil {
		t.Fatal(err)
	}
	rig.pay(t, j.ID, 3_000)
	rig.payUnusable(t, j.ID, 2_000)
	sub := rig.open(t, j.ID, 5_000)
	rig.pay(t, sub.ID, 1_000)

	v := rig.view(t, j.ID)
	p := v.PnL
	if p.PaidMinor != 6_000 || p.WastedMinor != 2_000 || p.Accepted != 2 || p.CostPerAcceptedMinor != 3_000 {
		t.Fatalf("paid 3000 + 2000 (unusable) + 1000 in the sub-task, two answers accepted: %+v", p)
	}
	if p.ValueMinor == nil || *p.ValueMinor != 10_000 || p.ProfitMinor == nil || *p.ProfitMinor != 4_000 {
		t.Fatalf("worth 10000, paid 6000: %+v", p)
	}
	if v.Children[0].PnL.PaidMinor != 1_000 || v.Children[0].PnL.ProfitMinor != nil {
		t.Errorf("the sub-task has its own figures and no value of its own: %+v", v.Children[0].PnL)
	}

	// A value set later, then cleared.
	nine := int64(9_000)
	if v, err := rig.jobs.SetValue(ctx, j.ID, &nine); err != nil || *v.PnL.ProfitMinor != 3_000 {
		t.Fatalf("value 9000: %+v %v", v, err)
	}
	if v, err := rig.jobs.SetValue(ctx, j.ID, nil); err != nil || v.PnL.ProfitMinor != nil {
		t.Fatalf("cleared: %+v %v", v, err)
	}
	neg := int64(-1)
	if _, err := rig.jobs.SetValue(ctx, j.ID, &neg); err == nil {
		t.Error("a negative value is refused")
	}

	// The agent's P&L counts the same payments, and the valued tasks.
	if _, err := rig.jobs.SetValue(ctx, j.ID, &value); err != nil {
		t.Fatal(err)
	}
	a, err := rig.jobs.AgentPnL(ctx, "user_1", "pass_agent_1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if a.PaidMinor != 6_000 || a.WastedMinor != 2_000 || a.Accepted != 2 || a.Tasks != 1 || a.ValuedTasks != 1 || a.ValueMinor != 10_000 || a.ProfitMinor != 4_000 {
		t.Fatalf("agent: %+v", a)
	}
	if _, err := rig.jobs.AgentPnL(ctx, "user_2", "pass_agent_1", 30); err == nil {
		t.Error("only the pass's owner reads its P&L")
	}
}

// A reused answer counts as saved for the task that asked, and for its agent.
func TestPnL_ReuseIsSaved(t *testing.T) {
	ctx := context.Background()
	rig, _ := keepingRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	jobs := NewJobPassService(rig.econRig.store, rig.econRig.passes, nil)
	jobs.SetPnL(rig.econRig.store)
	jobs.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := rig.do(t, "agent_1", "w1", false, "alpha"); err != nil {
		t.Fatal(err)
	}
	j, err := jobs.Open(ctx, OpenJob{UserID: "user_1", PassID: "pass_agent_1", Spec: jobpass.Spec{Label: "Again", BudgetMinor: 50_000}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: doSpec("w2"), Candidates: []routing.Candidate{cand("alpha")}, JobID: j.ID})
	if err != nil || got.Report.Reused == nil {
		t.Fatalf("reused: %+v %v", got, err)
	}
	v, err := jobs.View(ctx, j.ID)
	if err != nil || v.PnL.SavedMinor != 3_000 || v.PnL.Reused != 1 || v.PnL.PaidMinor != 0 {
		t.Fatalf("the task saved 3000 and paid nothing: %+v %v", v.PnL, err)
	}
	a, err := jobs.AgentPnL(ctx, "user_1", "pass_agent_1", 30)
	if err != nil || a.SavedMinor != 3_000 || a.Reused != 1 || a.PaidMinor != 3_000 {
		t.Fatalf("the agent paid once and saved once: %+v %v", a, err)
	}
}
