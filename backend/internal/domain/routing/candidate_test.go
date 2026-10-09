package routing

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func x402Candidate() Candidate {
	return Candidate{
		Capability: "Solana.Token-Risk", Provider: "  Example ", ExecutionType: ExecX402,
		Endpoint: "HTTPS://API.Example.com:443/v1/risk/", Method: "post",
		PriceMinor: 3000, Asset: "usdc", Network: "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
		Sources: []DiscoverySource{SourcePaySh},
	}
}

func mustNormalize(t *testing.T, c Candidate) Candidate {
	t.Helper()
	n, err := c.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return n
}

func TestCandidateNormalizeCanonicalForm(t *testing.T) {
	c := mustNormalize(t, x402Candidate())
	if c.Capability != "solana.token-risk" || c.Provider != "example" || c.Method != "POST" || c.Asset != "USDC" || c.Network != "solana" {
		t.Errorf("fields not canonical: %+v", c)
	}
	if c.Endpoint != "https://api.example.com/v1/risk" {
		t.Errorf("endpoint = %q", c.Endpoint)
	}
	if !strings.HasPrefix(c.ID, "cand_") || len(c.ID) != len("cand_")+16 {
		t.Errorf("id shape: %q", c.ID)
	}
	// Normalizing twice changes nothing.
	if again := mustNormalize(t, c); !reflect.DeepEqual(again, c) {
		t.Errorf("Normalize isn't idempotent:\n%+v\n%+v", c, again)
	}
}

func TestCandidateIDIsDeterministicAndSourceIndependent(t *testing.T) {
	base := mustNormalize(t, x402Candidate())

	same := x402Candidate()
	same.Sources = []DiscoverySource{SourceWeb} // another source found the same offer
	same.Endpoint = "https://api.example.com/v1/risk#section"
	same.Name = "A different display name"
	same.PriceMinor = 9999 // advertised price is not identity
	if got := mustNormalize(t, same).ID; got != base.ID {
		t.Errorf("same offer must have the same ID across sources: %s vs %s", got, base.ID)
	}

	for name, mutate := range map[string]func(*Candidate){
		"network":  func(c *Candidate) { c.Network = "base"; c.AssetAddress = "" },
		"method":   func(c *Candidate) { c.Method = "GET" },
		"provider": func(c *Candidate) { c.Provider = "other" },
		"endpoint": func(c *Candidate) { c.Endpoint = "https://api.example.com/v2/risk" },
		"type":     func(c *Candidate) { c.ExecutionType = ExecHTTPAPI },
		"cap":      func(c *Candidate) { c.Capability = "solana.wallet-risk" },
	} {
		c := x402Candidate()
		mutate(&c)
		if got := mustNormalize(t, c).ID; got == base.ID {
			t.Errorf("a different %s must be a different candidate", name)
		}
	}
}

func TestCandidateRefusals(t *testing.T) {
	cases := map[string]func(*Candidate){
		"no sources":          func(c *Candidate) { c.Sources = nil },
		"unknown source":      func(c *Candidate) { c.Sources = []DiscoverySource{"rumour"} },
		"bad execution type":  func(c *Candidate) { c.ExecutionType = "carrier_pigeon" },
		"x402 needs endpoint": func(c *Candidate) { c.Endpoint = "" },
		"plain http":          func(c *Candidate) { c.Endpoint = "http://api.example.com/x" },
		"ftp":                 func(c *Candidate) { c.Endpoint = "ftp://api.example.com/x" },
		"credentials":         func(c *Candidate) { c.Endpoint = "https://user:pw@api.example.com/x" },
		"relative":            func(c *Candidate) { c.Endpoint = "/v1/risk" },
		"private ip":          func(c *Candidate) { c.Endpoint = "https://10.0.0.5/x" },
		"metadata ip":         func(c *Candidate) { c.Endpoint = "https://169.254.169.254/latest/meta-data" },
		"mapped private ip":   func(c *Candidate) { c.Endpoint = "https://[::ffff:192.168.1.1]/x" },
		"decimal ip":          func(c *Candidate) { c.Endpoint = "https://2130706433/x" },
		"hex ip":              func(c *Candidate) { c.Endpoint = "https://0x7f000001/x" },
		"long endpoint":       func(c *Candidate) { c.Endpoint = "https://api.example.com/" + strings.Repeat("a", 2100) },
		"bad method":          func(c *Candidate) { c.Method = "TELEPORT" },
		"negative price":      func(c *Candidate) { c.PriceMinor = -1 },
		"price without asset": func(c *Candidate) { c.Asset = "" },
		"bad slippage":        func(c *Candidate) { c.SlippageBps = 10_001 },
		"fractional output":   func(c *Candidate) { c.EstimatedOutput = "12.5" },
		"negative latency":    func(c *Candidate) { c.EstimatedLatencyMS = -1 },
		"bad requirements":    func(c *Candidate) { c.PaymentRequirements = json.RawMessage(`{nope`) },
		"huge requirements": func(c *Candidate) {
			c.PaymentRequirements = json.RawMessage(`"` + strings.Repeat("a", maxReqsBytes) + `"`)
		},
		"look-alike USDC": func(c *Candidate) {
			c.AssetAddress = "FakeUSDCMint1111111111111111111111111111111"
		},
		"bad history": func(c *Candidate) { c.History = &History{Calls: 3, SuccessRate: 1.5} },
		"bad name":    func(c *Candidate) { c.Name = strings.Repeat("n", 121) },
		"bad provider": func(c *Candidate) {
			c.Provider = "has spaces"
		},
	}
	for name, mutate := range cases {
		c := x402Candidate()
		mutate(&c)
		if _, err := c.Normalize(); err == nil {
			t.Errorf("%s should be refused: %+v", name, c)
		}
	}
}

func TestRealUSDCAddressIsAccepted(t *testing.T) {
	c := x402Candidate()
	c.AssetAddress = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	if _, err := c.Normalize(); err != nil {
		t.Errorf("Circle's mainnet USDC must be accepted: %v", err)
	}
}

func TestLoopbackIsAllowedForDevelopment(t *testing.T) {
	for _, ep := range []string{"http://localhost:8080/api/v1/sandbox/x402/token-risk", "http://127.0.0.1:9000/x", "https://[::1]:8443/p", "http://svc.localhost/x"} {
		c := x402Candidate()
		c.Endpoint = ep
		if _, err := c.Normalize(); err != nil {
			t.Errorf("%s should be accepted for local development: %v", ep, err)
		}
	}
}

func TestSolanaSwapNeedsNoEndpoint(t *testing.T) {
	c := Candidate{Capability: "swap.spot", Provider: "jupiter", ExecutionType: ExecSolanaSwap, Network: "solana", Sources: []DiscoverySource{SourceNative}}
	n := mustNormalize(t, c)
	if n.Endpoint != "" || n.ID == "" {
		t.Errorf("native swap candidate: %+v", n)
	}
}

func TestEndpointNormalization(t *testing.T) {
	cases := map[string]string{
		"https://API.example.com":                "https://api.example.com",
		"https://api.example.com/":               "https://api.example.com",
		"https://api.example.com:443/a/b/":       "https://api.example.com/a/b",
		"https://api.example.com:8443/a":         "https://api.example.com:8443/a",
		"https://api.example.com/a?x=1&y=2#frag": "https://api.example.com/a?x=1&y=2",
		"http://localhost:80/a":                  "http://localhost/a",
		"https://[::1]:8443/p":                   "https://[::1]:8443/p",
		"  https://api.example.com/a  ":          "https://api.example.com/a",
	}
	for in, want := range cases {
		got, err := normalizeEndpoint(in)
		if err != nil || got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestTrust(t *testing.T) {
	mk := func(srcs []DiscoverySource, h *History) Candidate {
		c := x402Candidate()
		c.Sources, c.History = srcs, h
		return mustNormalize(t, c)
	}
	known := &History{Calls: ObservedMinCalls, SuccessRate: 0.2, ValidRate: 1, P50LatencyMS: 100, P95LatencyMS: 200}
	few := &History{Calls: ObservedMinCalls - 1}
	cases := []struct {
		name string
		c    Candidate
		want Trust
	}{
		{"native adapter", mk([]DiscoverySource{SourceNative}, nil), TrustNative},
		{"operator pinned", mk([]DiscoverySource{SourceConfigured, SourceWeb}, nil), TrustNative},
		{"executed often", mk([]DiscoverySource{SourceWeb}, known), TrustObserved},
		{"in a registry", mk([]DiscoverySource{SourceCircle}, few), TrustListed},
		{"registry and web", mk([]DiscoverySource{SourceWeb, SourcePaySh}, nil), TrustListed},
		{"web only", mk([]DiscoverySource{SourceWeb}, few), TrustUnverified},
		{"own history only", mk([]DiscoverySource{SourceHistory}, nil), TrustUnverified},
	}
	for _, tc := range cases {
		if got := tc.c.Trust(); got != tc.want {
			t.Errorf("%s: trust = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDedupeMergesTheSameOfferFromTwoSources(t *testing.T) {
	web := x402Candidate()
	web.Sources, web.PriceMinor, web.Asset, web.Name, web.EstimatedLatencyMS = []DiscoverySource{SourceWeb}, 0, "", "web-found name", 800
	web.DiscoveredAt = t0
	reg := x402Candidate()
	reg.Sources, reg.Name, reg.SourceRef = []DiscoverySource{SourceCircle}, "Registry Name", "circle:entry/42"
	reg.DiscoveredAt = t0.Add(time.Hour)

	out, dropped := Dedupe([]Candidate{web, reg})
	if len(dropped) != 0 || len(out) != 1 {
		t.Fatalf("want one merged candidate, got %d (dropped %v)", len(out), dropped)
	}
	m := out[0]
	if !slices.Equal(m.Sources, []DiscoverySource{SourceCircle, SourceWeb}) {
		t.Errorf("sources should list the most trusted first: %v", m.Sources)
	}
	if m.Name != "Registry Name" || m.SourceRef != "circle:entry/42" {
		t.Errorf("the better-sourced candidate wins conflicts: %+v", m)
	}
	if m.PriceMinor != 3000 || m.Asset != "USDC" {
		t.Errorf("price and asset come together from the same source: %d %s", m.PriceMinor, m.Asset)
	}
	if m.EstimatedLatencyMS != 800 {
		t.Errorf("gaps are filled from the other source: latency %d", m.EstimatedLatencyMS)
	}
	if !m.DiscoveredAt.Equal(t0) {
		t.Errorf("first discovery time is kept: %v", m.DiscoveredAt)
	}
	if m.Trust() != TrustListed {
		t.Errorf("merging a web hit into a registry entry makes it listed: %s", m.Trust())
	}
}

func TestDedupeDropsBadCandidatesWithoutSinkingTheRest(t *testing.T) {
	good := x402Candidate()
	bad := x402Candidate()
	bad.Provider = "sketchy"
	bad.Endpoint = "https://169.254.169.254/latest/meta-data"
	fake := x402Candidate()
	fake.Provider = "fakeusdc"
	fake.AssetAddress = "NotCirclesUSDC11111111111111111111111111111"

	out, dropped := Dedupe([]Candidate{bad, good, fake})
	if len(out) != 1 || out[0].Provider != "example" {
		t.Fatalf("the good candidate must survive: %+v", out)
	}
	if len(dropped) != 2 || dropped[0].Provider != "sketchy" || dropped[1].Provider != "fakeusdc" {
		t.Fatalf("both bad candidates should be reported, in input order: %+v", dropped)
	}
	if !strings.Contains(dropped[0].Reason, "private") {
		t.Errorf("reason should say why: %q", dropped[0].Reason)
	}
	if strings.Contains(dropped[0].Reason, "169.254") {
		t.Errorf("reasons must not echo the rejected URL: %q", dropped[0].Reason)
	}
}

func TestDedupeIsOrderIndependent(t *testing.T) {
	// a and d are equally trusted sources that disagree; c is the open web
	// with yet another view; b is a different offer entirely.
	a := x402Candidate()
	a.Name, a.EstimatedLatencyMS = "paysh listing", 0
	b := x402Candidate()
	b.Provider, b.Sources = "other", []DiscoverySource{SourceCircle}
	c := x402Candidate()
	c.Sources, c.Name, c.EstimatedLatencyMS = []DiscoverySource{SourceWeb}, "web name", 900
	d := x402Candidate()
	d.Sources, d.Name, d.EstimatedLatencyMS = []DiscoverySource{SourceX402}, "x402 listing", 400
	in := []Candidate{a, b, c, d}

	want, _ := Dedupe(in)
	if len(want) != 2 {
		t.Fatalf("a, c and d are one offer; b is another: got %d", len(want))
	}
	permute(len(in), func(idx []int) {
		shuffled := make([]Candidate, len(in))
		for i, j := range idx {
			shuffled[i] = in[j]
		}
		got, _ := Dedupe(shuffled)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order %v merged differently:\n%+v\n%+v", idx, got, want)
		}
	})
	for _, m := range want {
		if m.Provider == "example" && (m.Name != "paysh listing" || m.EstimatedLatencyMS != 400) {
			t.Errorf("paysh wins the name (alphabetical among equals) and the first gap-filler supplies latency: %+v", m)
		}
	}
}

// permute calls fn with every ordering of 0..n-1.
func permute(n int, fn func([]int)) {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			fn(slices.Clone(idx))
			return
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			rec(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	rec(0)
}

func TestNormalizeDoesNotAliasTheCallersData(t *testing.T) {
	c := x402Candidate()
	c.History = &History{Calls: 9, SuccessRate: 0.9, ValidRate: 1, P50LatencyMS: 10, P95LatencyMS: 20}
	c.PaymentRequirements = json.RawMessage(`{"scheme":"exact"}`)
	n := mustNormalize(t, c)
	c.History.Calls = 1
	c.PaymentRequirements[2] = 'X'
	if n.History.Calls != 9 || string(n.PaymentRequirements) != `{"scheme":"exact"}` {
		t.Errorf("normalised candidate shares memory with its input: %+v %s", n.History, n.PaymentRequirements)
	}
}

func TestHistoryValidate(t *testing.T) {
	ok := History{Calls: 4812, SuccessRate: 0.9971, ValidRate: 0.991, P50LatencyMS: 241, P95LatencyMS: 684, AvgQuality: 97.2, AvgCostMinor: 3000}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a realistic history must pass: %v", err)
	}
	for name, h := range map[string]History{
		"negative calls":        {Calls: -1},
		"rate over one":         {SuccessRate: 1.01},
		"nan rate":              {ValidRate: nan()},
		"quality over 100":      {AvgQuality: 100.5},
		"p95 below p50":         {P50LatencyMS: 300, P95LatencyMS: 200},
		"negative latency":      {P50LatencyMS: -1},
		"negative average cost": {AvgCostMinor: -1},
	} {
		if h.Validate() == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

func nan() float64 { return math.NaN() }
