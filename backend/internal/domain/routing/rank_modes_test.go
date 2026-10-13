package routing

import "testing"

// The same three providers, ranked three ways: Cheapest, Fastest and Balanced
// (ModeAuto) each pick their own winner from one set of options, and ranking
// is deterministic, so asking twice gives the same order.
func TestRank_TheThreeModesPickDifferentWinnersFromTheSameOptions(t *testing.T) {
	flaky := &History{Calls: 40, SuccessRate: 0.7, ValidRate: 0.9, P50LatencyMS: 900, P95LatencyMS: 2000, AvgQuality: 55}
	solid := &History{Calls: 40, SuccessRate: 0.99, ValidRate: 1, P50LatencyMS: 400, P95LatencyMS: 700, AvgQuality: 92}
	quick := &History{Calls: 40, SuccessRate: 0.9, ValidRate: 0.95, P50LatencyMS: 80, P95LatencyMS: 160, AvgQuality: 70}
	opts := func() []Option {
		return []Option{
			offer("cheap", SourceCircle, 2_400, 900, flaky),
			offer("fast", SourceCircle, 9_000, 80, quick),
			offer("solid", SourceCircle, 2_500, 400, solid),
		}
	}
	for _, c := range []struct {
		mode Mode
		want string
	}{
		{ModeCheapest, "cheap,solid,fast"},
		{ModeFastest, "fast,solid,cheap"},
		{ModeAuto, "solid"},
	} {
		got := Rank(c.mode, opts())
		if c.mode == ModeAuto {
			if got[0].Quote.CandidateID != c.want {
				t.Errorf("%s: first is %s, want %s (%s)", c.mode, got[0].Quote.CandidateID, c.want, ids(got))
			}
		} else if ids(got) != c.want {
			t.Errorf("%s: %s, want %s", c.mode, ids(got), c.want)
		}
		if again := Rank(c.mode, opts()); ids(again) != ids(got) {
			t.Errorf("%s is not deterministic: %s then %s", c.mode, ids(got), ids(again))
		}
		for i, s := range got {
			if s.Rank != i+1 {
				t.Errorf("%s: step %d has rank %d", c.mode, i, s.Rank)
			}
		}
	}
}
