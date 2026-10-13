package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/intel"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// The dry run is what a person decides on, so it must name the provider the
// execution would pay: same record of each provider, same environment, same
// rules. And it shows the best offer each way, for the console's cards.

// seedHistory gives a provider n delivered, paid calls of latencyMS each.
func seedHistory(t *testing.T, rig *execRig, provider string, n, latencyMS int) {
	t.Helper()
	c, err := cand(provider).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		at := time.Now().Add(-time.Duration(i+1) * time.Minute)
		rec := StoredExecution{Result: routing.ExecutionResult{
			ID: fmt.Sprintf("xres_%s_%d", provider, i), IntentID: "eint_seed", ReservationID: fmt.Sprintf("rsv_%s_%d", provider, i), Attempt: 1,
			CandidateID: c.ID, Provider: provider, Capability: c.Capability, ExecutionType: routing.ExecX402,
			StartedAt: at, CompletedAt: at.Add(time.Duration(latencyMS) * time.Millisecond), LatencyMS: int64(latencyMS),
			QuotedCostMinor: 3_000, ActualCostMinor: 3_000, Payment: routing.PaymentSettled, Delivery: econ.FulfillmentFulfilled, Network: "sandbox", Asset: "USDC",
		}}
		if err := rig.store.SaveResult(context.Background(), "user_1", rec); err != nil {
			t.Fatal(err)
		}
	}
}

func riskSpec(window, strategy string) econ.Spec {
	return econ.Spec{Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: window, Currency: "USDC", BudgetMaxMinor: 50_000,
		ProviderPolicy: econ.ProviderPolicy{Strategy: strategy}}
}

func TestSimulate_RanksOnTheSameRecordAsTheExecution(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	// Both quote the same price and the same estimated speed; only Algebra's
	// own record tells them apart: beta answers in 80 ms, alpha in 900 ms.
	seedHistory(t, rig, "alpha", 6, 900)
	seedHistory(t, rig, "beta", 6, 80)
	cands := []routing.Candidate{cand("alpha"), cand("beta")}

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: riskSpec("parity", "fastest"), Candidates: cands, LiveQuotes: true})
	if err != nil {
		t.Fatal(err)
	}
	if sim.WouldPay == nil || sim.WouldPay.Provider != "beta" {
		t.Fatalf("the dry run must rank on the record: would pay %+v", sim.WouldPay)
	}
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_1", Spec: riskSpec("parity", "fastest"), Candidates: cands})
	if err != nil || !res.Report.Delivered {
		t.Fatalf("execute: %v %+v", err, res)
	}
	if paid := res.Report.Attempts[0].Result.Provider; paid != sim.WouldPay.Provider {
		t.Fatalf("the dry run named %s and the execution paid %s", sim.WouldPay.Provider, paid)
	}

	// The cards: one per way of choosing, each with the speed it was ranked
	// on and where that speed comes from.
	if len(sim.Recommendations) != 3 {
		t.Fatalf("three recommendations: %+v", sim.Recommendations)
	}
	byWay := map[string]Recommendation{}
	for _, r := range sim.Recommendations {
		byWay[r.Strategy] = r
	}
	fast := byWay[econ.StrategyFastest]
	if fast.Provider != "beta" || fast.LatencyMS != 80 || fast.LatencySource != routing.LatencyObserved || fast.Calls != 6 || fast.Decision != "ALLOW" {
		t.Fatalf("fastest card: %+v", fast)
	}
	if _, ok := byWay[econ.StrategyCheapest]; !ok {
		t.Fatal("a cheapest card")
	}
	if _, ok := byWay[econ.StrategyBalanced]; !ok {
		t.Fatal("a balanced card")
	}
	if len(sim.Compared) != 2 {
		t.Fatalf("every offer is compared: %+v", sim.Compared)
	}
}

func TestSimulate_AnUnlistedPriceIsNotFree(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	priced := cand("alpha")
	priced.PriceMinor, priced.Asset = 3_000, "USDC"
	unlisted := cand("beta") // the catalog states no price
	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_1", Spec: riskSpec("unlisted", "cheapest"), Candidates: []routing.Candidate{unlisted, priced}})
	if err != nil {
		t.Fatal(err)
	}
	if sim.WouldPay == nil || sim.WouldPay.Provider != "alpha" {
		t.Fatalf("an unstated price must not rank as the cheapest: %+v", sim.WouldPay)
	}
	for _, c := range sim.Candidates {
		if c.Provider == "beta" && c.PriceSource != "unlisted" {
			t.Fatalf("the unlisted price says so: %+v", c)
		}
	}
}

// envPass adds an agent whose pass belongs to a project's environment.
func envPass(t *testing.T, rig *execRig, agentID, env string, c spendpass.Controls) {
	t.Helper()
	rig.econRig.agents.put(&agent.Identity{ID: agentID, UserID: "user_1", Name: agentID, ClientID: "spend-pass:custom"})
	now := time.Now()
	if err := rig.econRig.passes.Create(context.Background(), &spendpass.Pass{
		ID: "pass_" + agentID, UserID: "user_1", AgentID: agentID, Label: agentID, AgentKind: spendpass.AgentCustom, Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		ProjectID: "proj_1", Environment: env, Controls: c,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSimulate_AProjectPassStaysInItsEnvironment(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	envPass(t, rig, "agent_sbx", "sandbox", spendpass.Controls{})
	onMainnet := cand("gamma")
	onMainnet.Network = "solana"
	cands := []routing.Candidate{onMainnet, cand("alpha")}

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_sbx", Spec: riskSpec("env", "cheapest"), Candidates: cands})
	if err != nil {
		t.Fatal(err)
	}
	if sim.WouldPay == nil || sim.WouldPay.Provider != "alpha" {
		t.Fatalf("a sandbox pass's dry run must not name a mainnet provider: %+v", sim.WouldPay)
	}
	for _, r := range sim.Recommendations {
		if r.Provider == "gamma" {
			t.Fatalf("a provider on another network is never recommended: %+v", r)
		}
	}
	spec := riskSpec("env-devnet", "cheapest")
	spec.Constraints.AllowedNetworks = []string{"solana-devnet"}
	sim, err = rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_sbx", Spec: spec, Candidates: cands})
	if err != nil || sim.Decision != "BLOCK" || len(sim.Reasons) == 0 || sim.Reasons[0] != DenyEnvironmentNetwork {
		t.Fatalf("asking a sandbox pass for devnet is refused, as it is when executing: %v %+v", err, sim)
	}
}

type fakeTrust map[string]intel.ProviderTrust

func (f fakeTrust) ProviderTrust(_ context.Context, ids []string) (map[string]intel.ProviderTrust, error) {
	out := map[string]intel.ProviderTrust{}
	for _, id := range ids {
		if t, ok := f[id]; ok {
			out[id] = t
		}
	}
	return out, nil
}

func TestUnverifiedRule_ApproveHoldsAndFallsBackToAVerifiedProvider(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.econRig.svc.SetProviderTrust(fakeTrust{"alpha": {Endpoints: 1, Verified: 1}})
	envPass(t, rig, "agent_v", "", spendpass.Controls{NewProviders: spendpass.NewProvidersAllow, UnverifiedProviders: spendpass.UnverifiedApprove})
	rig.runner.behaviors["alpha"] = rig.delivers(goodBody)
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	rig.runner.costs["beta"] = 1_000 // the cheaper one is unverified
	cands := []routing.Candidate{cand("beta"), cand("alpha")}

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_v", Spec: riskSpec("v1", "cheapest"), Candidates: cands, LiveQuotes: true})
	if err != nil {
		t.Fatal(err)
	}
	if sim.WouldPay == nil || sim.WouldPay.Provider != "alpha" || sim.Decision != "ALLOW" {
		t.Fatalf("the verified provider is the one that runs without asking: %+v", sim)
	}
	for _, r := range sim.Recommendations {
		if r.Strategy == econ.StrategyCheapest && (r.Provider != "beta" || r.Decision != "HOLD") {
			t.Fatalf("the cheapest card is the unverified one, and it would wait: %+v", r)
		}
	}
	res, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_v", Spec: riskSpec("v1", "cheapest"), Candidates: cands})
	if err != nil || !res.Report.Delivered || res.Report.Attempts[len(res.Report.Attempts)-1].Result.Provider != "alpha" || rig.runner.ran("beta") != 0 {
		t.Fatalf("an unverified provider is never paid without a yes: %v %+v", err, res)
	}

	// Only the unverified one: the request waits for the person, then runs.
	spec := riskSpec("v2", "cheapest")
	var rej *ReservationRejected
	if _, err := rig.exec.Do(ctx, DoRequest{AgentID: "agent_v", Spec: spec, Candidates: []routing.Candidate{cand("beta")}}); !errors.As(err, &rej) || rej.Reason != RejectApproval ||
		len(rej.ReasonCodes) == 0 || rej.ReasonCodes[0] != spendpass.ReasonUnverifiedNeedsApproval {
		t.Fatalf("held for the person: %v", err)
	}
	v, _, _ := rig.econRig.svc.CreateIntent(ctx, "agent_v", spec)
	if _, err := rig.econRig.svc.Approve(ctx, "user_1", v.ID); err != nil {
		t.Fatal(err)
	}
	res, err = rig.exec.Do(ctx, DoRequest{AgentID: "agent_v", Spec: spec, Candidates: []routing.Candidate{cand("beta")}})
	if err != nil || !res.Report.Delivered || rig.runner.ran("beta") != 1 {
		t.Fatalf("after the yes it is paid: %v %+v", err, res)
	}
}

func TestUnverifiedRule_DenyNeverPaysAnUnverifiedProvider(t *testing.T) {
	ctx := context.Background()
	rig := newExecRig(t)
	rig.econRig.svc.SetProviderTrust(fakeTrust{})
	envPass(t, rig, "agent_d", "", spendpass.Controls{NewProviders: spendpass.NewProvidersAllow, UnverifiedProviders: spendpass.UnverifiedDeny})
	rig.runner.behaviors["beta"] = rig.delivers(goodBody)
	cands := []routing.Candidate{cand("beta")}

	sim, err := rig.exec.Simulate(ctx, SimulateRequest{AgentID: "agent_d", Spec: riskSpec("d1", ""), Candidates: cands})
	if err != nil || sim.Decision != "BLOCK" {
		t.Fatalf("the dry run says no: %v %+v", err, sim)
	}
	_, err = rig.exec.Do(ctx, DoRequest{AgentID: "agent_d", Spec: riskSpec("d1", ""), Candidates: cands})
	var nr *NoRoute
	if !errors.As(err, &nr) || rig.runner.ran("beta") != 0 {
		t.Fatalf("refused, with nothing to approve and nothing paid: %v", err)
	}
	// The low-level reservation refuses it too: the rule doesn't depend on the router.
	in, _, _ := rig.econRig.svc.CreateIntent(ctx, "agent_d", riskSpec("d2", ""))
	var rej *ReservationRejected
	if _, err := rig.econRig.svc.Reserve(ctx, "agent_d", in.ID, ReserveRequest{ProviderID: "beta", Rail: "sandbox", QuoteMinor: 3_000}); !errors.As(err, &rej) ||
		rej.Reason != RejectAuthorityDenied || rej.ReasonCodes[0] != spendpass.ReasonUnverifiedNotAllowed {
		t.Fatalf("a direct reservation is refused: %v", err)
	}
}
