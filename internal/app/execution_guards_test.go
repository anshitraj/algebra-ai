package app

import (
	"context"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
)

func guardCand(t *testing.T, provider string, listed int64, network string) routing.Candidate {
	t.Helper()
	c, err := routing.Candidate{
		Capability: "token.price", Provider: provider, ExecutionType: routing.ExecX402, Method: "GET",
		Endpoint: "https://" + provider + ".example/price", PriceMinor: listed, Asset: "USDC", Network: network,
		Sources: []routing.DiscoverySource{routing.SourceX402},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func guardQuote(c routing.Candidate, live int64) routing.Quote {
	return routing.Quote{CandidateID: c.ID, Provider: c.Provider, Cost: routing.Cost{ProviderMinor: live}}
}

func TestGuardRefusesALivePriceAboveTheListing(t *testing.T) {
	c := guardCand(t, "honest", 1_000, "solana")
	g := guards{}
	if r := g.after(c, guardQuote(c, 1_040)); r != nil {
		t.Fatalf("4%% over the listing is rounding, got %+v", r)
	}
	r := g.after(c, guardQuote(c, 2_000))
	if r == nil || r.Code != RejectPriceAboveListing {
		t.Fatalf("asking twice the listed price must be refused, got %+v", r)
	}
}

func TestGuardRefusesHoneypotPrices(t *testing.T) {
	var cands []routing.Candidate
	for _, p := range []string{"a", "b", "c", "d"} {
		cands = append(cands, guardCand(t, p, 10_000, "solana"))
	}
	trap := guardCand(t, "trap", 25_000_000, "solana")
	cands = append(cands, trap)
	s := &ExecutionService{now: time.Now}
	g := s.newGuards(context.Background(), cands)
	if g.median != 10_000 {
		t.Fatalf("median = %d", g.median)
	}
	view := &IntentView{Intent: econ.Intent{}}
	if _, r := g.before(view, trap); r == nil || r.Code != RejectPriceOutlier {
		t.Fatalf("a $25 call where peers charge $0.01 must be refused, got %+v", r)
	}
	if _, r := g.before(view, cands[0]); r != nil {
		t.Fatalf("a usual price must pass, got %+v", r)
	}
	// With no peers to compare, only an absurd price is refused.
	lone := guards{}
	if lone.outlier(25_000_000) != "" {
		t.Fatal("without peers $25 isn't an outlier by itself")
	}
	if lone.outlier(2_000 * 1_000_000) == "" {
		t.Fatal("$2,000 for one call is a trap whatever the peers charge")
	}
}

func TestGuardSkipsDownProvidersAndOtherNetworks(t *testing.T) {
	up := guardCand(t, "up", 1_000, "solana")
	down := guardCand(t, "down", 1_000, "solana")
	devnet := guardCand(t, "dev", 1_000, "solana-devnet")
	g := guards{health: map[string]EndpointHealth{
		down.ID: {Status: HealthDown, Failures: 3, Error: "timed out"},
		up.ID:   {Status: HealthUp, LatencyMS: 240},
	}}
	view := &IntentView{Intent: econ.Intent{Constraints: econ.Constraints{AllowedNetworks: []string{"solana"}}}}
	if _, r := g.before(view, down); r == nil || r.Code != RejectProviderDown {
		t.Fatalf("down provider: %+v", r)
	}
	if _, r := g.before(view, devnet); r == nil || r.Code != routing.RejectNetwork {
		t.Fatalf("devnet candidate for a mainnet-only intent: %+v", r)
	}
	got, r := g.before(view, up)
	if r != nil || got.EstimatedLatencyMS != 240 {
		t.Fatalf("up provider should pass with the probe's latency, got %+v %d", r, got.EstimatedLatencyMS)
	}
}

type fakeClasses map[string][]routing.Candidate

func (f fakeClasses) ClassCandidates(_ context.Context, id string) ([]routing.Candidate, error) {
	return f[id], nil
}

func TestResolveClassOrdersAndNarrows(t *testing.T) {
	cheap := guardCand(t, "cheap", 1_000, "solana")
	pricey := guardCand(t, "pricey", 9_000, "solana")
	unknown := guardCand(t, "unknown", 0, "solana")
	mine, _ := routing.Candidate{Capability: "token.price", Provider: "mine", ExecutionType: routing.ExecX402,
		Endpoint: "https://mine.example/p", Sources: []routing.DiscoverySource{routing.SourceConfigured}}.Normalize()
	r := CandidateResolver{
		Configured: map[string][]routing.Candidate{"mine": {mine}},
		Classes:    fakeClasses{"token.price": {unknown, pricey, cheap}},
	}
	got, rej := r.Resolve(context.Background(), "token.price", nil, nil)
	if len(rej) != 0 || len(got) != 4 {
		t.Fatalf("got %d candidates, %v", len(got), rej)
	}
	want := []string{"mine", "cheap", "pricey", "unknown"}
	for i, w := range want {
		if got[i].Provider != w {
			t.Fatalf("order %d = %s, want %s", i, got[i].Provider, w)
		}
	}
	got, rej = r.Resolve(context.Background(), "token.price", []string{"pricey", "nobody"}, nil)
	if len(got) != 1 || got[0].Provider != "pricey" || len(rej) != 1 || rej[0].Provider != "nobody" {
		t.Fatalf("narrowing: %v %v", got, rej)
	}
}
