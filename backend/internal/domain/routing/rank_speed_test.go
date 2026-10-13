package routing

import (
	"strings"
	"testing"
)

// Fastest has to work before anyone has paid a provider: the unpaid price
// check is a real round trip, so it times a provider Algebra has no record
// of. And when nothing is timed at all, Fastest says it ranked by price.

func TestRankFastestTimesANewProviderByItsPriceCheck(t *testing.T) {
	slow, quick := offer("slow", SourceCircle, 1_000, 0, nil), offer("quick", SourceCircle, 2_000, 0, nil)
	slow.Quote.PriceCheckMS, quick.Quote.PriceCheckMS = 900, 40
	got := Rank(ModeFastest, []Option{slow, quick})
	if ids(got) != "quick,slow" {
		t.Fatalf("the quicker price check wins: %s", ids(got))
	}
	if got[0].Score.LatencyMS != 40 || got[0].Score.LatencySource != LatencyPriceCheck {
		t.Fatalf("the speed and where it came from: %+v", got[0].Score)
	}
	if !strings.Contains(strings.Join(got[0].Score.Notes, " "), "price check") {
		t.Fatalf("the note says how it was timed: %v", got[0].Score.Notes)
	}
}

func TestRankFastestPrefersPaidCallsOverAPriceCheck(t *testing.T) {
	measured := offer("measured", SourceCircle, 1_000, 0, &History{Calls: 8, SuccessRate: 1, ValidRate: 1, P50LatencyMS: 120, P95LatencyMS: 200, AvgQuality: 90})
	measured.Quote.PriceCheckMS = 30
	got := Rank(ModeFastest, []Option{measured})
	if got[0].Score.LatencyMS != 120 || got[0].Score.LatencySource != LatencyObserved || got[0].Score.Calls != 8 {
		t.Fatalf("its own paid calls decide: %+v", got[0].Score)
	}
}

func TestRankFastestSaysWhenNoSpeedIsKnown(t *testing.T) {
	got := Rank(ModeFastest, []Option{offer("a", SourceCircle, 2_000, 0, nil), offer("b", SourceCircle, 1_000, 0, nil)})
	if ids(got) != "b,a" {
		t.Fatalf("with no speed known it falls back to price: %s", ids(got))
	}
	if head := got[0].Score.Notes[0]; strings.Contains(head, "fastest") || !strings.Contains(head, "no speed known") {
		t.Fatalf("it must not claim a speed it never measured: %q", head)
	}
}
