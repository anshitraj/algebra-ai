package routing

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"
)

// offer builds a ranked-ready option: a candidate from one source, priced at
// cost, expected to take latency ms (0 = unknown).
func offer(id string, src DiscoverySource, cost int64, latency int, h *History) Option {
	return Option{
		Candidate: Candidate{ID: id, Provider: id, Sources: []DiscoverySource{src}, History: h, PriceMinor: cost, Asset: "USDC"},
		Quote:     Quote{ID: "q-" + id, CandidateID: id, Cost: Cost{ProviderMinor: cost}, Asset: "USDC", Network: "solana", EstimatedLatencyMS: latency},
	}
}

func ids(steps []PlanStep) string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Quote.CandidateID
	}
	return strings.Join(out, ",")
}

func TestRankCheapestPutsTheLowestLiveCostFirst(t *testing.T) {
	opts := []Option{
		offer("b", SourceCircle, 5_000, 200, nil),
		offer("a", SourceCircle, 3_000, 900, nil),
		offer("c", SourceCircle, 4_000, 100, nil),
	}
	got := Rank(ModeCheapest, opts)
	if ids(got) != "a,c,b" {
		t.Fatalf("cheapest first: %s", ids(got))
	}
	for i, s := range got {
		if s.Rank != i+1 {
			t.Errorf("rank %d at position %d", s.Rank, i)
		}
	}
	if got[0].Score.Total != 1 || got[1].Score.Total >= got[0].Score.Total {
		t.Errorf("the cheapest scores one and the rest less: %+v", got)
	}
	if !strings.Contains(strings.Join(got[0].Score.Notes, "|"), "cheapest of 3 offers") {
		t.Errorf("the winner says why: %v", got[0].Score.Notes)
	}
}

func TestRankCheapestCountsTheWholeCostNotJustTheProvidersPrice(t *testing.T) {
	cheapButDear := offer("cheap-price", SourceCircle, 1_000, 100, nil)
	cheapButDear.Quote.Cost.NetworkFeeMinor, cheapButDear.Quote.Cost.BridgeFeeMinor = 4_000, 3_000
	plain := offer("plain", SourceCircle, 6_000, 100, nil)
	if got := Rank(ModeCheapest, []Option{cheapButDear, plain}); ids(got) != "plain,cheap-price" {
		t.Fatalf("fees count towards cost: %s", ids(got))
	}
}

func TestRankFastestPrefersMeasuredSpeedAndKnownBeforeUnknown(t *testing.T) {
	observed := &History{Calls: 20, SuccessRate: 0.95, ValidRate: 1, P50LatencyMS: 150, P95LatencyMS: 400, AvgQuality: 80}
	claimed := offer("claims-fast", SourceCircle, 3_000, 50, nil) // says 50 ms, never measured
	// History beats the claim: this provider answered in 150 ms over 20 calls
	// even though its quote says 700.
	measured := offer("measured", SourceCircle, 3_000, 700, observed)
	unknown := offer("unknown", SourceCircle, 1_000, 0, nil)
	got := Rank(ModeFastest, []Option{unknown, measured, claimed})
	if ids(got) != "claims-fast,measured,unknown" {
		t.Fatalf("known speeds first, shortest first, unknown last: %s", ids(got))
	}
	// And a measured 150 ms really does outrank a claimed 300.
	slower := offer("claims-300", SourceCircle, 1_000, 300, nil)
	if got := Rank(ModeFastest, []Option{slower, measured}); ids(got) != "measured,claims-300" {
		t.Errorf("the record outweighs the claim: %s", ids(got))
	}
}

func TestRankAutoWeighsReliabilityAndCostTogether(t *testing.T) {
	good := &History{Calls: 30, SuccessRate: 0.97, ValidRate: 0.99, P50LatencyMS: 400, P95LatencyMS: 900, AvgQuality: 90}
	flaky := &History{Calls: 30, SuccessRate: 0.62, ValidRate: 0.9, P50LatencyMS: 400, P95LatencyMS: 900, AvgQuality: 60}
	// The flaky one is a little cheaper; the good one should still win in AUTO.
	opts := []Option{offer("flaky", SourceCircle, 2_900, 400, flaky), offer("good", SourceCircle, 3_000, 400, good)}
	if got := Rank(ModeAuto, opts); ids(got) != "good,flaky" {
		t.Fatalf("auto prefers the provider that delivers: %s", ids(got))
	}
	// CHEAPEST takes the lower price: it only sets aside providers that mostly
	// fail, and 62% isn't that.
	if got := Rank(ModeCheapest, opts); ids(got) != "flaky,good" {
		t.Errorf("cheapest takes the lower price: %s", ids(got))
	}
	// The weights sum to one, so a Total reads as "how near the best possible".
	if w := sum(Rank(ModeAuto, opts)[0].Score.Weights); w < 0.999 || w > 1.001 {
		t.Errorf("auto weights sum to one: %v", w)
	}
}

func sum(m map[string]float64) float64 {
	var t float64
	for _, v := range m {
		t += v
	}
	return t
}

func TestRankSetsAsideProvidersThatMostlyFailWhateverTheMode(t *testing.T) {
	bad := &History{Calls: 10, SuccessRate: 0.3, ValidRate: 1, P50LatencyMS: 50, P95LatencyMS: 80, AvgQuality: 90}
	opts := []Option{offer("bad-but-cheap-and-fast", SourceCircle, 1_000, 50, bad), offer("ok", SourceCircle, 9_000, 900, nil)}
	for _, m := range []Mode{ModeCheapest, ModeFastest, ModeAuto} {
		got := Rank(m, opts)
		if ids(got) != "ok,bad-but-cheap-and-fast" {
			t.Errorf("%s: a provider that fails 7 in 10 comes last: %s", m, ids(got))
		}
		if !strings.Contains(strings.Join(got[1].Score.Notes, "|"), "demoted") {
			t.Errorf("%s: the demotion is explained: %v", m, got[1].Score.Notes)
		}
	}
	// Too few calls to say: three failures in three is not an observed record.
	few := &History{Calls: 3, SuccessRate: 0, ValidRate: 0}
	if got := Rank(ModeCheapest, []Option{offer("few", SourceCircle, 1_000, 0, few), offer("ok", SourceCircle, 9_000, 0, nil)}); ids(got) != "few,ok" {
		t.Errorf("below ObservedMinCalls nothing is demoted: %s", ids(got))
	}
}

func TestRankTestNetworksComeAfterRealOnes(t *testing.T) {
	devnet := offer("devnet", SourceCircle, 1_000, 100, nil)
	devnet.Quote.Network, devnet.Quote.Test = "solana-devnet", true
	mainnet := offer("mainnet", SourceCircle, 9_000, 900, nil)
	for _, m := range []Mode{ModeCheapest, ModeFastest, ModeAuto} {
		if got := Rank(m, []Option{devnet, mainnet}); ids(got) != "mainnet,devnet" {
			t.Errorf("%s: real money before test money: %s", m, ids(got))
		}
	}
}

func TestRankStillSetsAsideFailingProvidersOnATestNetwork(t *testing.T) {
	bad := &History{Calls: 10, SuccessRate: 0.1, ValidRate: 1}
	mk := func(id string, cost int64, h *History) Option {
		o := offer(id, SourceCircle, cost, 100, h)
		o.Quote.Network, o.Quote.Test = "solana-devnet", true
		return o
	}
	opts := []Option{mk("bad-and-cheap", 1_000, bad), mk("fine", 9_000, nil)}
	for _, m := range []Mode{ModeCheapest, ModeFastest, ModeAuto} {
		if got := Rank(m, opts); ids(got) != "fine,bad-and-cheap" {
			t.Errorf("%s: the tiers add up: %s", m, ids(got))
		}
	}
	// And a failing provider on a real network still beats a fine one on a test network.
	real := offer("real-but-flaky", SourceCircle, 9_000, 100, bad)
	if got := Rank(ModeCheapest, []Option{mk("fine", 1_000, nil), real}); ids(got) != "real-but-flaky,fine" {
		t.Errorf("real money comes before test money even when it fails more: %s", ids(got))
	}
}

func TestRankNotesAProviderThatAsksMoreThanItsListing(t *testing.T) {
	honest := offer("honest", SourceCircle, 5_000, 300, nil)
	liar := offer("liar", SourceCircle, 5_000, 300, nil)
	liar.Candidate.PriceMinor = 1_000 // listed at 0.001, asks 0.005
	got := Rank(ModeAuto, []Option{liar, honest})
	if ids(got) != "honest,liar" {
		t.Fatalf("the honest one ranks first at the same price: %s", ids(got))
	}
	if got[1].Score.Components[ScorePriceHonesty] >= 0.5 || got[0].Score.Components[ScorePriceHonesty] != 1 {
		t.Errorf("honesty scores: %+v %+v", got[0].Score.Components, got[1].Score.Components)
	}
	if !strings.Contains(strings.Join(got[1].Score.Notes, "|"), "listed at 0.001 USDC") {
		t.Errorf("the mismatch is said plainly: %v", got[1].Score.Notes)
	}
	// Rounding (under 5%) is not dishonesty.
	near := offer("near", SourceCircle, 1_040, 300, nil)
	near.Candidate.PriceMinor = 1_000
	if h := Rank(ModeAuto, []Option{near})[0].Score.Components[ScorePriceHonesty]; h != 1 {
		t.Errorf("a 4%% difference is rounding: %v", h)
	}
}

func TestRankTrustLetsAKnownProviderBeatAnUnknownOneAtTheSamePrice(t *testing.T) {
	native := offer("native", SourceConfigured, 3_000, 300, nil)
	listed := offer("listed", SourceCircle, 3_000, 300, nil)
	web := offer("web", SourceWeb, 3_000, 300, nil)
	if got := Rank(ModeAuto, []Option{web, listed, native}); ids(got) != "native,listed,web" {
		t.Fatalf("native, then listed, then an unverified web find: %s", ids(got))
	}
}

func TestRankSmoothsAShortRecordTowardsThePrior(t *testing.T) {
	// One lucky call must not make a provider look perfect.
	lucky := offer("lucky", SourceCircle, 3_000, 300, &History{Calls: 1, SuccessRate: 1, ValidRate: 1})
	proven := offer("proven", SourceCircle, 3_000, 300, &History{Calls: 40, SuccessRate: 0.95, ValidRate: 1, P50LatencyMS: 300, P95LatencyMS: 500, AvgQuality: 80})
	got := Rank(ModeAuto, []Option{lucky, proven})
	if ids(got) != "proven,lucky" {
		t.Fatalf("forty good calls beat one: %s", ids(got))
	}
	if r := got[1].Score.Components[ScoreReliability]; r >= 0.95 {
		t.Errorf("one call isn't proof: %v", r)
	}
}

func TestRankIsStable(t *testing.T) {
	// Options that tie on every measure keep the order they were given in.
	twins := []Option{offer("first", SourceCircle, 3_000, 300, nil), offer("second", SourceCircle, 3_000, 300, nil), offer("third", SourceCircle, 3_000, 300, nil)}
	for _, m := range []Mode{ModeCheapest, ModeFastest, ModeAuto} {
		if got := ids(Rank(m, twins)); got != "first,second,third" {
			t.Errorf("%s: ties keep the given order: %s", m, got)
		}
		if got := ids(Rank(m, []Option{twins[2], twins[0], twins[1]})); got != "third,first,second" {
			t.Errorf("%s: and it is the caller's order that counts: %s", m, got)
		}
	}
}

func TestRankDoesNotDependOnInputOrderExceptForTies(t *testing.T) {
	// Every option differs on cost, so no order of arrival can change the answer.
	var opts []Option
	for i := 0; i < 8; i++ {
		opts = append(opts, offer(string(rune('a'+i)), SourceCircle, int64(3_000+i*700), 300+(i%3)*100, nil))
	}
	want := ids(Rank(ModeCheapest, opts))
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 20; n++ {
		shuffled := slices.Clone(opts)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := ids(Rank(ModeCheapest, shuffled)); got != want {
			t.Fatalf("input order changed the ranking: %s vs %s", got, want)
		}
	}
}

func TestRankEmptyAndSingle(t *testing.T) {
	if Rank(ModeAuto, nil) != nil {
		t.Error("nothing to rank")
	}
	one := Rank(ModeCheapest, []Option{offer("only", SourceCircle, 0, 0, nil)})
	if len(one) != 1 || one[0].Rank != 1 || one[0].Score.Total != 1 {
		t.Errorf("a single free offer: %+v", one)
	}
	if len(one[0].Score.Notes) == 0 || strings.Contains(strings.Join(one[0].Score.Notes, "|"), "of 1 offers") {
		t.Errorf("no headline for a single offer: %v", one[0].Score.Notes)
	}
}

func TestRankedStepsMakeAValidPlan(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	var opts []Option
	for _, id := range []string{"x", "y", "z"} {
		o := offer(id, SourceCircle, 3_000, 200, nil)
		o.Quote.Capability, o.Quote.Provider, o.Quote.ExecutionType = "solana.token-risk", id, ExecX402
		o.Quote.QuotedAt, o.Quote.ValidUntil = now, now.Add(time.Minute)
		opts = append(opts, o)
	}
	p, err := NewPlan("plan_1", PlanSpec{Capability: "solana.token-risk", Mode: ModeAuto, Steps: Rank(ModeAuto, opts)}, now)
	if err != nil {
		t.Fatalf("ranked steps form a plan: %v", err)
	}
	if p.Primary().Rank != 1 || len(p.Fallbacks()) != 2 {
		t.Errorf("plan shape: %+v", p)
	}
}
