package routing

import (
	"strings"
	"testing"
)

// A provider that is cheaper on its price tag but takes the money and fails
// often costs more per accepted answer, and CHEAPEST says so.
func TestRankCheapestIsPerAcceptedAnswer(t *testing.T) {
	// 0.0008 a call, accepted 50% of the time, 45% paid for nothing.
	leaky := offer("leaky", SourceCircle, 800, 200, &History{Calls: 40, SuccessRate: 0.5, WastedRate: 0.45, ValidRate: 0.5, P50LatencyMS: 200, P95LatencyMS: 400})
	// 0.001 a call, accepted 95% of the time.
	steady := offer("steady", SourceCircle, 1_000, 200, &History{Calls: 40, SuccessRate: 0.95, WastedRate: 0.02, ValidRate: 0.98, P50LatencyMS: 200, P95LatencyMS: 400})
	got := Rank(ModeCheapest, []Option{leaky, steady})
	if ids(got) != "steady,leaky" {
		t.Fatalf("the cheaper price tag loses when it is paid for nothing half the time: %s", ids(got))
	}
	s, l := got[0].Score, got[1].Score
	// steady: 1000 × (0.95+0.02)/0.95 ≈ 1021 (smoothed a little towards the prior).
	if s.CostPerAcceptedMinor < 1_000 || s.CostPerAcceptedMinor > 1_060 {
		t.Errorf("steady per accepted answer: %d", s.CostPerAcceptedMinor)
	}
	// leaky: 800 × (0.5+0.45)/0.5 ≈ 1520.
	if l.CostPerAcceptedMinor < 1_400 || l.CostPerAcceptedMinor > 1_650 {
		t.Errorf("leaky per accepted answer: %d", l.CostPerAcceptedMinor)
	}
	if !strings.Contains(strings.Join(l.Notes, "|"), "per accepted answer: 51% of its calls accepted") {
		t.Errorf("the note explains the figure: %v", l.Notes)
	}
}

// Failures that cost nothing (a provider down before payment) add attempts,
// not money: with no waste, the cost per accepted answer is the price.
func TestCostPerAcceptedCountsOnlyPaidFailures(t *testing.T) {
	free := offer("free-fail", SourceCircle, 1_000, 200, &History{Calls: 400, SuccessRate: 0.6, WastedRate: 0, ValidRate: 1, P50LatencyMS: 200, P95LatencyMS: 400})
	st := Rank(ModeCheapest, []Option{free})[0].Score
	if st.CostPerAcceptedMinor < 1_000 || st.CostPerAcceptedMinor > 1_010 {
		t.Errorf("failures before payment cost no money: %d", st.CostPerAcceptedMinor)
	}
	if acc, waste, cpa := acceptance(1_000, 0.7, &History{Calls: 10, SuccessRate: 0}); acc <= 0 || waste <= 0 || cpa > 50_000 {
		t.Errorf("a provider that never delivers is capped, not infinite: %v %v %d", acc, waste, cpa)
	}
	if _, _, cpa := acceptance(0, 0.7, nil); cpa != 0 {
		t.Errorf("free stays free: %d", cpa)
	}
}

// A plan's expectation walks the fallbacks: a step is only paid for when every
// step before it failed.
func TestExpectationCountsFallbacks(t *testing.T) {
	steps := []PlanStep{
		{Quote: Quote{Cost: Cost{ProviderMinor: 1_000}}, Score: Score{AcceptRate: 0.9, WasteRate: 0.05}},
		{Quote: Quote{Cost: Cost{ProviderMinor: 2_000}}, Score: Score{AcceptRate: 0.8, WasteRate: 0.1}},
	}
	cost, accept, per := Expectation(steps)
	// 1000×0.95 + 0.1×2000×0.9 = 950 + 180 = 1130; accepted 0.9 + 0.1×0.8 = 0.98.
	if cost != 1_130 || accept != 0.98 || per != 1_153 {
		t.Errorf("want 1130, 0.98, 1153; got %d, %v, %d", cost, accept, per)
	}
	if c, a, p := Expectation(nil); c != 0 || a != 0 || p != 0 {
		t.Error("nothing planned, nothing expected")
	}
}
