package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

// strategyIntent opens an intent with its own window, so several can be asked
// for in one test without being "the same outcome" as one another.
func (r *execRig) strategyIntent(t *testing.T, strategy, window string, mutate ...func(*econ.Spec)) *IntentView {
	t.Helper()
	spec := econ.Spec{
		Capability: "solana.token-risk", Input: json.RawMessage(`{"mint":"SOL"}`), Window: window, Currency: "USDC", BudgetMaxMinor: 50_000,
		ProviderPolicy: econ.ProviderPolicy{Strategy: strategy},
	}
	for _, m := range mutate {
		m(&spec)
	}
	v, _, err := r.econRig.svc.CreateIntent(context.Background(), "agent_1", spec)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ranRoute makes three providers that all deliver, differing in price and
// speed, so the strategy alone decides who is paid.
func (r *execRig) threeProviders() {
	for _, p := range []string{"slow-cheap", "balanced", "fast-dear"} {
		r.runner.behaviors[p] = r.delivers(goodBody)
	}
	r.runner.costs = map[string]int64{"slow-cheap": 2_000, "balanced": 3_000, "fast-dear": 6_000}
	r.runner.latencies = map[string]int{"slow-cheap": 900, "balanced": 400, "fast-dear": 80}
}

func (r *execRig) paid(t *testing.T) string {
	t.Helper()
	var who []string
	for _, p := range []string{"slow-cheap", "balanced", "fast-dear"} {
		if r.runner.ran(p) > 0 {
			who = append(who, p)
		}
	}
	return strings.Join(who, ",")
}

func TestRouter_TheIntentsStrategyDecidesWhoIsPaid(t *testing.T) {
	for _, tc := range []struct{ strategy, want string }{
		{"cheapest", "slow-cheap"},
		{"fastest", "fast-dear"},
		{"auto", "slow-cheap"}, // cost and trust dominate when everything delivers equally
		{"", "slow-cheap"},     // no strategy is auto
	} {
		t.Run(tc.strategy, func(t *testing.T) {
			rig := newExecRig(t)
			rig.threeProviders()
			in := rig.strategyIntent(t, tc.strategy, "w-"+tc.strategy)
			// Named in the order that would be tried if nothing ranked them.
			rep, err := rig.run(t, in.ID, "fast-dear", "balanced", "slow-cheap")
			if err != nil || !rep.Delivered {
				t.Fatalf("delivered: %v %v", rep, err)
			}
			if got := rig.paid(t); got != tc.want {
				t.Errorf("%q: paid %s, want %s", tc.strategy, got, tc.want)
			}
			if rep.Plan.Primary().Quote.Provider != tc.want || len(rep.Plan.Steps) != 3 {
				t.Errorf("the plan is ranked, best first: %s of %d", rep.Plan.Primary().Quote.Provider, len(rep.Plan.Steps))
			}
		})
	}
}

func TestRouter_ExplainsItsChoiceToTheAgentAndTheLog(t *testing.T) {
	rig := newExecRig(t)
	rig.threeProviders()
	in := rig.strategyIntent(t, "cheapest", "w-explain")
	rep, err := rig.run(t, in.ID, "fast-dear", "balanced", "slow-cheap")
	if err != nil {
		t.Fatal(err)
	}
	out := OutcomeOf(nil, rep)
	if out.Routing == nil || out.Routing.Mode != "CHEAPEST" || out.Routing.PlanHash != rep.Plan.Hash || len(out.Routing.Offers) != 3 {
		t.Fatalf("routing summary: %+v", out.Routing)
	}
	first := out.Routing.Offers[0]
	if first.Rank != 1 || first.Provider != "slow-cheap" || first.CostMinor != 2_000 || first.Trust != "native" ||
		!strings.Contains(strings.Join(first.Notes, "|"), "cheapest of 3 offers") {
		t.Errorf("the first offer says why: %+v", first)
	}
	// No payee or payment requirements in an explanation.
	if b, _ := json.Marshal(out.Routing); strings.Contains(string(b), "payee-") {
		t.Errorf("the summary must not carry payment terms: %s", b)
	}
	// The log carries the ranking too.
	v, _ := rig.econRig.svc.View(context.Background(), in.ID, true)
	var ranking any
	for _, e := range v.Events {
		if e.Event == "routing.plan_selected" {
			ranking = e.Data["ranking"]
		}
	}
	b, _ := json.Marshal(ranking)
	var list []map[string]any
	if json.Unmarshal(b, &list) != nil || len(list) != 3 || list[0]["provider"] != "slow-cheap" {
		t.Errorf("the plan_selected event lists the ranking: %s", b)
	}
}

// seedHistory writes n finished attempts for a candidate, as earlier requests
// would have left them.
func (r *execRig) seedHistory(t *testing.T, provider string, n int, ok bool, latencyMS int64) {
	t.Helper()
	c, err := cand(provider).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		started := time.Now().Add(-time.Duration(i+1) * time.Hour)
		res := routing.ExecutionResult{
			ID: "seed-" + provider + "-" + string(rune('a'+i)), IntentID: "seed", CandidateID: c.ID, Provider: provider, Capability: c.Capability,
			ExecutionType: routing.ExecX402, StartedAt: started, CompletedAt: started.Add(time.Second), LatencyMS: latencyMS,
			Payment: routing.PaymentSettled, Delivery: econ.FulfillmentFulfilled, QuotedCostMinor: 3_000, ActualCostMinor: 3_000,
		}
		if !ok {
			res.Payment, res.Delivery, res.ActualCostMinor = routing.PaymentNotAttempted, econ.FulfillmentNotFulfilled, 0
			res.Failure = &routing.Failure{Class: routing.FailProvider, Message: "seeded failure"}
		}
		if err := r.store.SaveResult(context.Background(), "user_1", StoredExecution{Result: res}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouter_LearnsFromWhatHappenedBefore(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["flaky"] = rig.delivers(goodBody)
	rig.runner.behaviors["steady"] = rig.delivers(goodBody)
	rig.runner.costs = map[string]int64{"flaky": 1_000, "steady": 3_000}
	// The cheaper provider has failed 8 of its last 8 calls.
	rig.seedHistory(t, "flaky", 8, false, 0)
	in := rig.strategyIntent(t, "cheapest", "w-learn")

	rep, err := rig.run(t, in.ID, "flaky", "steady")
	if err != nil || !rep.Delivered {
		t.Fatalf("delivered: %v %v", rep, err)
	}
	if rig.runner.ran("steady") != 1 || rig.runner.ran("flaky") != 0 {
		t.Errorf("a provider that keeps failing is tried last even when it is cheapest: steady=%d flaky=%d", rig.runner.ran("steady"), rig.runner.ran("flaky"))
	}
	notes := strings.Join(rep.Plan.Steps[1].Score.Notes, "|")
	if rep.Plan.Steps[1].Quote.Provider != "flaky" || !strings.Contains(notes, "0 of 8 calls delivered") || !strings.Contains(notes, "demoted") {
		t.Errorf("and the plan says why: %v", rep.Plan.Steps[1].Score.Notes)
	}
}

func TestRouter_HonoursTheIntentsMinimumReliabilityNowThatHistoryExists(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.behaviors["flaky"] = rig.delivers(goodBody)
	rig.runner.behaviors["steady"] = rig.delivers(goodBody)
	rig.seedHistory(t, "flaky", 4, true, 300)
	rig.seedHistory(t, "flaky", 6, false, 0) // 4 of 10
	in := rig.strategyIntent(t, "auto", "w-minrel", func(s *econ.Spec) { s.Constraints.MinReliabilityPct = 80 })

	rep, err := rig.run(t, in.ID, "flaky", "steady")
	if err != nil || !rep.Delivered {
		t.Fatalf("delivered: %v %v", rep, err)
	}
	var found bool
	for _, r := range rep.Rejected {
		if r.Provider == "flaky" && r.Code == routing.RejectReliability {
			found = true
		}
	}
	if !found || rig.runner.ran("flaky") != 0 || len(rep.Plan.Steps) != 1 {
		t.Errorf("a provider below the person's reliability floor is set aside: %+v", rep.Rejected)
	}
}

func TestRouter_PlansTheBestFiveAndSaysWhoWasOutranked(t *testing.T) {
	rig := newExecRig(t)
	var names []string
	for i := 0; i < 7; i++ {
		n := "p" + string(rune('a'+i))
		names = append(names, n)
		rig.runner.behaviors[n] = rig.delivers(goodBody)
		rig.runner.costs[n] = int64(2_000 + i*500) // pa is the cheapest
	}
	in := rig.strategyIntent(t, "cheapest", "w-outranked")
	// Named dearest first, to show it is the ranking and not the order that decides.
	slices.Reverse(names)
	rep, err := rig.run(t, in.ID, names...)
	if err != nil || !rep.Delivered {
		t.Fatalf("delivered: %v %v", rep, err)
	}
	if len(rep.Plan.Steps) != routing.MaxPlanSteps || rep.Plan.Primary().Quote.Provider != "pa" {
		t.Fatalf("the plan holds the five cheapest, cheapest first: %d, %s", len(rep.Plan.Steps), rep.Plan.Primary().Quote.Provider)
	}
	var outranked []string
	for _, r := range rep.Rejected {
		if r.Code == RejectOutranked {
			outranked = append(outranked, r.Provider)
		}
	}
	slices.Sort(outranked)
	if strings.Join(outranked, ",") != "pf,pg" {
		t.Errorf("the two dearest were outranked: %v", outranked)
	}
}

func TestRouter_PricesProvidersAFewAtATimeAndKeepsAnOrder(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.quoteDelay = 40 * time.Millisecond
	var names []string
	for i := 0; i < 8; i++ {
		n := "q" + string(rune('a'+i))
		names = append(names, n)
		rig.runner.behaviors[n] = rig.delivers(goodBody)
	}
	in := rig.strategyIntent(t, "auto", "w-concurrent")
	started := time.Now()
	rep, err := rig.run(t, in.ID, names...)
	took := time.Since(started)
	if err != nil || !rep.Delivered {
		t.Fatalf("delivered: %v %v", rep, err)
	}
	rig.runner.mu.Lock()
	peak := rig.runner.maxInflight
	rig.runner.mu.Unlock()
	if peak < 2 || peak > quoteConcurrency {
		t.Errorf("prices are worked out in parallel, within the limit: peak %d", peak)
	}
	// Eight prices at 40 ms each, four at a time: about 80 ms, not 320.
	if took > 250*time.Millisecond {
		t.Errorf("pricing eight providers took %s; it should overlap", took)
	}
	// All tie, so the order the caller gave is kept.
	if rep.Plan.Primary().Quote.Provider != "qa" {
		t.Errorf("ties keep the caller's order: %s", rep.Plan.Primary().Quote.Provider)
	}
}

func TestRouter_AProviderThatPanicsWhilePricingCostsOnlyItself(t *testing.T) {
	rig := newExecRig(t)
	rig.runner.quotePanics["boom"] = true
	rig.runner.behaviors["ok"] = rig.delivers(goodBody)
	in := rig.strategyIntent(t, "auto", "w-panic")

	rep, err := rig.run(t, in.ID, "boom", "ok")
	if err != nil || !rep.Delivered {
		t.Fatalf("the other provider delivers: %v %v", rep, err)
	}
	var crashed bool
	for _, r := range rep.Rejected {
		if r.Provider == "boom" && r.Code == routing.RejectUnquotable && strings.Contains(r.Detail, "crashed") {
			crashed = true
		}
	}
	if !crashed {
		t.Errorf("the crash is reported as an unpriceable provider: %+v", rep.Rejected)
	}
}

func TestRouter_ASlowProviderIsSkippedNotWaitedFor(t *testing.T) {
	rig := newExecRig(t)
	rig.exec.quoteTimeout = 60 * time.Millisecond
	rig.runner.quoteDelay = 5 * time.Second // every price takes longer than the deadline...
	rig.runner.behaviors["a"] = rig.delivers(goodBody)
	in := rig.strategyIntent(t, "auto", "w-slow")

	started := time.Now()
	_, err := rig.run(t, in.ID, "a")
	var nr *NoRoute
	if !errors.As(err, &nr) {
		t.Fatalf("nothing could be priced in time: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Errorf("the deadline cut the wait short: %s", time.Since(started))
	}
	if len(nr.Rejected) != 1 || nr.Rejected[0].Code != routing.RejectUnquotable {
		t.Errorf("and it says so: %+v", nr.Rejected)
	}
}
