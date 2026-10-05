package routing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

func quoteFor(c Candidate, id string, total int64) Quote {
	return Quote{
		ID: id, CandidateID: c.ID, Capability: c.Capability, Provider: c.Provider, ExecutionType: c.ExecutionType,
		Endpoint: c.Endpoint, Cost: Cost{ProviderMinor: total}, Asset: "USDC", Network: "solana",
		PayTo: "PayeeAddr111", Semantics: econ.SemanticsPrepaidExact, EstimatedLatencyMS: 300,
		Requirements: json.RawMessage(`{"scheme":"exact","network":"solana","payTo":"PayeeAddr111","maxAmountRequired":"3000"}`),
		QuotedAt:     t0, ValidUntil: t0.Add(30 * time.Second),
	}
}

func candidateNamed(t *testing.T, provider string) Candidate {
	t.Helper()
	c := x402Candidate()
	c.Provider = provider
	c.Endpoint = "https://" + provider + ".example.com/v1/risk"
	return mustNormalize(t, c)
}

func TestCostTotal(t *testing.T) {
	c := Cost{ProviderMinor: 3000, NetworkFeeMinor: 5, BridgeFeeMinor: 100, SlippageMinor: 20}
	if got := c.Total(); got != 3125 {
		t.Errorf("total = %d, want 3125", got)
	}
	if got := (Cost{ProviderMinor: math.MaxInt64, NetworkFeeMinor: 1}).Total(); got != math.MaxInt64 {
		t.Errorf("total must saturate, got %d", got)
	}
	if got := (Cost{ProviderMinor: 10, NetworkFeeMinor: -50}).Total(); got != 10 {
		t.Errorf("a negative component counts as zero, got %d", got)
	}
	if (Cost{BridgeFeeMinor: -1}).Validate() == nil {
		t.Error("negative components must be refused")
	}
}

func TestQuoteValidate(t *testing.T) {
	c := candidateNamed(t, "birdeye")
	if err := quoteFor(c, "quo_1", 3000).Validate(); err != nil {
		t.Fatalf("a good quote must pass: %v", err)
	}
	cases := map[string]func(*Quote){
		"no id":              func(q *Quote) { q.ID = "" },
		"no candidate":       func(q *Quote) { q.CandidateID = "" },
		"no provider":        func(q *Quote) { q.Provider = "" },
		"bad type":           func(q *Quote) { q.ExecutionType = "x" },
		"negative cost":      func(q *Quote) { q.Cost.NetworkFeeMinor = -1 },
		"priced, no asset":   func(q *Quote) { q.Asset = "" },
		"priced, no network": func(q *Quote) { q.Network = "" },
		"valid before made":  func(q *Quote) { q.ValidUntil = q.QuotedAt },
		"bad latency":        func(q *Quote) { q.EstimatedLatencyMS = -1 },
		"bad slippage":       func(q *Quote) { q.SlippageBps = 10_001 },
		"bad impact":         func(q *Quote) { q.PriceImpactBps = -1 },
		"fractional output":  func(q *Quote) { q.ExpectedOutput = "1.5" },
		"bad requirements":   func(q *Quote) { q.Requirements = json.RawMessage(`{x`) },
		"huge requirements":  func(q *Quote) { q.Requirements = json.RawMessage(`"` + strings.Repeat("a", maxReqsBytes) + `"`) },
		"capability case":    func(q *Quote) { q.Capability = "Solana.Token-Risk" },
		"bad semantics":      func(q *Quote) { q.Semantics = "SOMETIMES" },
		"look-alike asset":   func(q *Quote) { q.AssetAddress = "NotCirclesUSDC11111111111111111111111111111" },
	}
	for name, mutate := range cases {
		q := quoteFor(c, "quo_1", 3000)
		mutate(&q)
		if q.Validate() == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	free := quoteFor(c, "quo_free", 0)
	free.Asset, free.Network = "", ""
	if err := free.Validate(); err != nil {
		t.Errorf("a free quote needs no asset or network: %v", err)
	}
}

func TestQuoteExpiry(t *testing.T) {
	q := quoteFor(candidateNamed(t, "birdeye"), "quo_1", 3000)
	if q.Expired(t0) || q.Expired(t0.Add(29*time.Second)) {
		t.Error("quote is payable until its deadline")
	}
	if !q.Expired(t0.Add(30 * time.Second)) {
		t.Error("quote is expired at its deadline")
	}
}

func TestQuoteHashCommitsToTermsNotIdentity(t *testing.T) {
	c := candidateNamed(t, "birdeye")
	base := quoteFor(c, "quo_1", 3000)

	again := quoteFor(c, "quo_2", 3000)
	again.QuotedAt, again.ValidUntil = t0.Add(time.Hour), t0.Add(time.Hour+time.Minute)
	again.Requirements = json.RawMessage("{ \"maxAmountRequired\":\"3000\", \"payTo\":\"PayeeAddr111\", \"network\":\"solana\", \"scheme\":\"exact\" }")
	if base.Hash() != again.Hash() {
		t.Errorf("a re-quote with the same terms must hash the same (id, times and JSON whitespace don't count):\n%s\n%s", base.Hash(), again.Hash())
	}
	for name, mutate := range map[string]func(*Quote){
		"price":    func(q *Quote) { q.Cost.ProviderMinor = 3001 },
		"fee":      func(q *Quote) { q.Cost.NetworkFeeMinor = 1 },
		"payee":    func(q *Quote) { q.PayTo = "SomeoneElse" },
		"network":  func(q *Quote) { q.Network = "base" },
		"asset":    func(q *Quote) { q.Asset = "USDT" },
		"endpoint": func(q *Quote) { q.Endpoint = "https://evil.example.com/v1/risk" },
		"reqs":     func(q *Quote) { q.Requirements = json.RawMessage(`{"scheme":"exact","payTo":"Attacker"}`) },
		"output":   func(q *Quote) { q.ExpectedOutput = "5" },
	} {
		q := quoteFor(c, "quo_1", 3000)
		mutate(&q)
		if q.Hash() == base.Hash() {
			t.Errorf("changing the %s must change the hash", name)
		}
	}
	if !strings.HasPrefix(base.Hash(), "sha256:") {
		t.Errorf("hash format: %s", base.Hash())
	}
}

func planSpec(t *testing.T, providers ...string) PlanSpec {
	t.Helper()
	spec := PlanSpec{IntentID: "eint_1", IntentHash: "sha256:abc", Capability: "solana.token-risk", Mode: ModeAuto}
	for i, p := range providers {
		c := candidateNamed(t, p)
		spec.Steps = append(spec.Steps, PlanStep{Quote: quoteFor(c, "quo_"+p, int64(3000+i)), Trust: c.Trust(), Score: Score{Total: 0.9 - float64(i)/10}})
	}
	return spec
}

func TestNewPlanRanksByPositionAndSeals(t *testing.T) {
	spec := planSpec(t, "birdeye", "helius", "alchemy")
	spec.Steps[0].Rank, spec.Steps[1].Rank, spec.Steps[2].Rank = 9, 9, 9 // the caller's ranks are not trusted
	p, err := NewPlan("plan_1", spec, t0)
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range p.Steps {
		if st.Rank != i+1 {
			t.Errorf("step %d has rank %d", i, st.Rank)
		}
	}
	if p.Primary().Quote.Provider != "birdeye" || len(p.Fallbacks()) != 2 || p.Fallbacks()[0].Quote.Provider != "helius" {
		t.Errorf("primary/fallbacks wrong: %+v", p.Steps)
	}
	if !strings.HasPrefix(p.Hash, "sha256:") || p.Hash != p.computeHash() {
		t.Errorf("plan must be sealed: %s", p.Hash)
	}
	if !p.CreatedAt.Equal(t0) || p.IntentID != "eint_1" || p.Capability != "solana.token-risk" {
		t.Errorf("plan fields: %+v", p)
	}
	// The plan owns its steps: mutating the caller's slice afterwards doesn't reach it.
	spec.Steps[0].Quote.Provider = "tampered"
	if p.Steps[0].Quote.Provider != "birdeye" {
		t.Error("plan shares its steps with the caller")
	}
}

func TestPlanHashCommitsToOrderAndTerms(t *testing.T) {
	a, err := NewPlan("plan_a", planSpec(t, "birdeye", "helius"), t0)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := NewPlan("plan_other_id", planSpec(t, "birdeye", "helius"), t0.Add(time.Second))
	if a.Hash != same.Hash {
		t.Error("the plan's own ID and creation time are not part of what it commits to")
	}
	swapped, _ := NewPlan("plan_b", planSpec(t, "helius", "birdeye"), t0)
	if a.Hash == swapped.Hash {
		t.Error("a different order is a different plan")
	}
	cheaper := planSpec(t, "birdeye", "helius")
	cheaper.Steps[0].Quote.Cost.ProviderMinor = 1
	c, _ := NewPlan("plan_c", cheaper, t0)
	if a.Hash == c.Hash {
		t.Error("a different price is a different plan")
	}
	cheaper = planSpec(t, "birdeye", "helius")
	cheaper.Mode = ModeCheapest
	d, _ := NewPlan("plan_d", cheaper, t0)
	if a.Hash == d.Hash {
		t.Error("a different mode is a different plan")
	}
	// Scores explain; they are not committed to.
	explained := planSpec(t, "birdeye", "helius")
	explained.Steps[0].Score = Score{Total: 0.1, Notes: []string{"changed the explanation only"}}
	e, _ := NewPlan("plan_e", explained, t0)
	if a.Hash != e.Hash {
		t.Error("the explanation of a ranking is not part of what the plan commits to")
	}
}

func TestNewPlanRefusals(t *testing.T) {
	cases := map[string]func(*PlanSpec){
		"no steps":  func(s *PlanSpec) { s.Steps = nil },
		"too many":  func(s *PlanSpec) { s.Steps = append(s.Steps, s.Steps...) },
		"bad mode":  func(s *PlanSpec) { s.Mode = "SOMETIMES" },
		"bad cap":   func(s *PlanSpec) { s.Capability = "x" },
		"other cap": func(s *PlanSpec) { s.Capability = "swap.spot" },
		"duplicate": func(s *PlanSpec) { s.Steps[1].Quote.CandidateID = s.Steps[0].Quote.CandidateID },
		"expired quote": func(s *PlanSpec) {
			s.Steps[1].Quote.QuotedAt, s.Steps[1].Quote.ValidUntil = t0.Add(-time.Minute), t0
		},
		"invalid quote": func(s *PlanSpec) { s.Steps[0].Quote.ID = "" },
	}
	for name, mutate := range cases {
		spec := planSpec(t, "birdeye", "helius", "alchemy", "quicknode")
		spec.Steps = spec.Steps[:3]
		if name == "too many" {
			spec = planSpec(t, "a1", "a2", "a3", "a4", "a5", "a6")
		}
		mutate(&spec)
		if _, err := NewPlan("plan_x", spec, t0); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	// The depth limit is exactly MaxPlanSteps.
	if _, err := NewPlan("plan_max", planSpec(t, "a1", "a2", "a3", "a4", "a5"), t0); err != nil {
		t.Errorf("%d steps is allowed: %v", MaxPlanSteps, err)
	}
}

func TestPlanNextSkipsAttemptedAndExpired(t *testing.T) {
	spec := planSpec(t, "birdeye", "helius", "alchemy")
	spec.Steps[1].Quote.ValidUntil = t0.Add(5 * time.Second) // helius's quote lapses soon
	p, err := NewPlan("plan_1", spec, t0)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := p.Next(nil, t0)
	if !ok || first.Quote.Provider != "birdeye" {
		t.Fatalf("first choice: %+v %v", first, ok)
	}
	second, ok := p.Next([]string{first.Quote.CandidateID}, t0)
	if !ok || second.Quote.Provider != "helius" {
		t.Fatalf("after birdeye: %+v %v", second, ok)
	}
	// Ten seconds later helius's quote has lapsed: skip it rather than pay stale terms.
	third, ok := p.Next([]string{first.Quote.CandidateID}, t0.Add(10*time.Second))
	if !ok || third.Quote.Provider != "alchemy" {
		t.Fatalf("a lapsed quote must be skipped: %+v %v", third, ok)
	}
	if _, ok := p.Next([]string{first.Quote.CandidateID, second.Quote.CandidateID, third.Quote.CandidateID}, t0); ok {
		t.Error("nothing is left once every candidate was attempted")
	}
}
