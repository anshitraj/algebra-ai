package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

func (r *execRig) intentAt(t *testing.T, window string, max int64) *IntentView {
	t.Helper()
	v, _, err := r.svc.CreateIntent(context.Background(), "agent_1", econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: window, Currency: "USDC", BudgetMaxMinor: max,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *execRig) controls(t *testing.T, c spendpass.Controls) {
	t.Helper()
	if err := r.passes.SetControls(context.Background(), "pass_agent_1", c); err != nil {
		t.Fatal(err)
	}
}

func TestKillSwitch_StopsAPaymentAlreadyInFlight(t *testing.T) {
	rig := newExecRig(t)
	pass := NewSpendPassService(rig.passes, NewAgentService(rig.agents), nil)
	// The person hits the kill switch while the provider is being called,
	// after the attempt began and before payment authority is asked for.
	rig.runner.behaviors["alpha"] = func(ctx context.Context, call StepCall) StepObservation {
		if _, err := pass.KillSwitch(ctx, "user_1", true); err != nil {
			t.Fatal(err)
		}
		return rig.delivers(goodBody)(ctx, call)
	}
	in := rig.intent(t, "agent_1", 50_000)
	rep, err := rig.run(t, in.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Delivered || len(rep.Attempts) != 1 || rep.Attempts[0].Result.ActualCostMinor != 0 {
		t.Fatalf("a frozen pass must release no payment: %+v", rep.Attempts)
	}
	if rep.Intent.State != econ.StateOpen {
		t.Fatalf("nothing moved, so the intent is open again, not %s", rep.Intent.State)
	}
	// While frozen the agent can't even reserve.
	if _, err := rig.run(t, in.ID, "alpha"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Fatalf("frozen pass: %v", err)
	}
	frozen, live, _ := pass.KillSwitchState(context.Background(), "user_1")
	if frozen != 1 || live != 1 {
		t.Fatalf("state: %d of %d frozen", frozen, live)
	}
	// Lifting it lets the same outcome through.
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	if n, _ := pass.KillSwitch(context.Background(), "user_1", false); n != 1 {
		t.Fatalf("thawed %d passes", n)
	}
	if rep, err := rig.run(t, in.ID, "alpha"); err != nil || !rep.Delivered {
		t.Fatalf("after thawing: %v %+v", err, rep)
	}
}

func TestNewProviderGate_AsksTheFirstTimeThenTrusts(t *testing.T) {
	rig := newExecRig(t)
	rig.controls(t, spendpass.Controls{NewProviders: spendpass.NewProvidersApprove})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)

	in := rig.intentAt(t, "w1", 50_000)
	_, err := rig.run(t, in.ID, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectApproval || rej.State != econ.StateAwaitingApproval {
		t.Fatalf("a never-paid provider under the approve rule must wait for the person: %v", err)
	}
	if rig.runner.ran("alpha") != 0 {
		t.Fatal("nothing may be called before the person says yes")
	}
	if _, err := rig.svc.Approve(context.Background(), "user_1", in.ID); err != nil {
		t.Fatal(err)
	}
	if rep, err := rig.run(t, in.ID, "alpha"); err != nil || !rep.Delivered {
		t.Fatalf("approved: %v", err)
	}
	// Paid once, alpha is no stranger: the next outcome goes straight through.
	in2 := rig.intentAt(t, "w2", 50_000)
	if rep, err := rig.run(t, in2.ID, "alpha"); err != nil || !rep.Delivered {
		t.Fatalf("second outcome with a known provider: %v", err)
	}
}

func TestNewProviderGate_FallsBackToAProviderAlreadyPaid(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	rig.runner.costs["beta"] = 1_000 // cheaper, so ranked first
	// alpha is paid once while new providers are allowed.
	rig.controls(t, spendpass.Controls{NewProviders: spendpass.NewProvidersAllow})
	in := rig.intentAt(t, "w1", 50_000)
	if rep, err := rig.run(t, in.ID, "alpha"); err != nil || !rep.Delivered {
		t.Fatal(err)
	}
	// Now strangers are capped below what beta asks.
	rig.controls(t, spendpass.Controls{NewProviders: spendpass.NewProvidersCap, NewProviderCapMinor: 500})
	in2 := rig.intentAt(t, "w2", 50_000)
	rep, err := rig.run(t, in2.ID, "beta", "alpha")
	if err != nil || !rep.Delivered {
		t.Fatalf("should fall back to alpha: %v", err)
	}
	if got := rep.Final().Result.Provider; got != "alpha" {
		t.Fatalf("delivered by %s, want the provider already paid", got)
	}
	gated := false
	for _, r := range rep.Rejected {
		gated = gated || (r.Provider == "beta" && r.Code == RejectNewProviderGate)
	}
	if !gated || rig.runner.ran("beta") != 0 {
		t.Fatalf("beta must be held back by the gate, not called: %+v", rep.Rejected)
	}
}

func TestVelocity_StopsALoopingAgent(t *testing.T) {
	rig := newExecRig(t)
	rig.controls(t, spendpass.Controls{MaxCallsPerMinute: 2, NewProviders: spendpass.NewProvidersAllow})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	for i := 1; i <= 2; i++ {
		in := rig.intentAt(t, fmt.Sprintf("loop-%d", i), 50_000)
		if rep, err := rig.run(t, in.ID, "alpha"); err != nil || !rep.Delivered {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	in := rig.intentAt(t, "loop-3", 50_000)
	_, err := rig.run(t, in.ID, "alpha")
	var rej *ReservationRejected
	if !errors.As(err, &rej) || rej.Reason != RejectRateLimited {
		t.Fatalf("the third call in a minute must be refused: %v", err)
	}
	if rig.runner.ran("alpha") != 2 {
		t.Fatalf("alpha ran %d times", rig.runner.ran("alpha"))
	}
}

func TestControls_Normalize(t *testing.T) {
	c, err := spendpass.Controls{}.Normalize("USDC")
	if err != nil || c.MaxCallsPerMinute != spendpass.DefaultMaxCallsPerMinute || c.NewProviders != spendpass.NewProvidersCap ||
		c.NewProviderCapMinor != spendpass.DefaultNewProviderCapMinor {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if _, err := (spendpass.Controls{NewProviders: "yolo"}).Normalize("USDC"); err == nil {
		t.Fatal("unknown rule accepted")
	}
	if code, ok := c.NewProvider(60_000, false, false); code != spendpass.ReasonNewProviderOverCap || !ok {
		t.Fatalf("over cap: %s", code)
	}
	if code, _ := c.NewProvider(60_000, true, false); code != "" {
		t.Fatalf("a provider already paid is not new: %s", code)
	}
}

func TestSimulate_AnswersWithoutPayingAndExplains(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	// alpha has been paid before; beta never.
	rig.controls(t, spendpass.Controls{NewProviders: spendpass.NewProvidersAllow})
	in := rig.intentAt(t, "w1", 50_000)
	if rep, err := rig.run(t, in.ID, "alpha"); err != nil || !rep.Delivered {
		t.Fatal(err)
	}
	rig.controls(t, spendpass.Controls{NewProviders: spendpass.NewProvidersApprove})
	before := rig.runner.ran("alpha")

	alpha, beta := cand("alpha"), cand("beta")
	alpha.PriceMinor, alpha.Asset = 3_000, "USDC"
	beta.PriceMinor, beta.Asset = 2_000, "USDC"
	spec := econ.Spec{Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: "sim", Currency: "USDC", BudgetMaxMinor: 50_000}
	sim, err := rig.exec.Simulate(context.Background(), SimulateRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{beta, alpha}})
	if err != nil {
		t.Fatal(err)
	}
	if sim.Verdict != VerdictAllow || sim.WouldPay == nil || sim.WouldPay.Provider != "alpha" {
		t.Fatalf("verdict %s, would pay %+v", sim.Verdict, sim.WouldPay)
	}
	var betaV *CandidateVerdict
	for i := range sim.Candidates {
		if sim.Candidates[i].Provider == "beta" {
			betaV = &sim.Candidates[i]
		}
	}
	if betaV == nil || betaV.Verdict != VerdictRequireApproval {
		t.Fatalf("beta is new under the approve rule: %+v", betaV)
	}
	if rig.runner.ran("alpha") != before {
		t.Fatal("a dry run must never call a provider to buy anything")
	}

	// Frozen: the answer is DENY, with the reason.
	pass := NewSpendPassService(rig.passes, NewAgentService(rig.agents), nil)
	_, _ = pass.KillSwitch(context.Background(), "user_1", true)
	sim, err = rig.exec.Simulate(context.Background(), SimulateRequest{AgentID: "agent_1", Spec: spec, Candidates: []routing.Candidate{alpha}})
	if err != nil || sim.Verdict != VerdictDeny || len(sim.Reasons) == 0 || sim.Reasons[0] != spendpass.ReasonFrozen {
		t.Fatalf("frozen: %v %+v", err, sim)
	}
}
