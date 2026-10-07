package x402client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

func probeCandidate(t *testing.T, p *provider, network string) routing.Candidate {
	t.Helper()
	c, err := routing.Candidate{
		Capability: "solana.token-risk", Provider: "acme", ExecutionType: routing.ExecX402, Endpoint: p.srv.URL + "/risk", Method: "POST", Network: network,
		Sources: []routing.DiscoverySource{routing.SourceConfigured},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func probeOption(network, scheme, asset string, amount int) string {
	return fmt.Sprintf(`{"scheme":%q,"network":%q,"maxAmountRequired":"%d","resource":"https://x","payTo":"Payee1","maxTimeoutSeconds":60,"asset":%q}`, scheme, network, amount, asset)
}

// A probe reads what a provider asks, never pays, and calls a provider unpriced
// only when it offers nothing in USDC on Solana (or the sandbox, which has no chain).
func TestProbeReadsWhatAProviderAsks(t *testing.T) {
	for name, tc := range map[string]struct {
		options []string
		network string
		want    ProbeWant
	}{
		"solana mainnet, payable here":               {[]string{probeOption("solana", "exact", solUSDC, 5_000)}, "", ProbeWant{Priced: true, Price: 5_000, Network: "solana", Scheme: "exact", Payable: true}},
		"the cheapest of several":                    {[]string{probeOption("solana", "exact", solUSDC, 9_000), probeOption("solana", "exact", solUSDC, 2_000)}, "", ProbeWant{Priced: true, Price: 2_000, Network: "solana", Scheme: "exact", Payable: true}},
		"usage-based":                                {[]string{probeOption("solana", "upto", solUSDC, 50_000)}, "", ProbeWant{Priced: true, Price: 50_000, Network: "solana", Scheme: "upto", Payable: true}},
		"the sandbox, which has no chain":            {[]string{probeOption("sandbox", "exact", "USDC", 3_000)}, "sandbox", ProbeWant{Priced: true, Price: 3_000, Network: "sandbox", Scheme: "exact", Payable: true}},
		"the sandbox, asked about without a network": {[]string{probeOption("sandbox", "exact", "USDC", 3_000)}, "", ProbeWant{Priced: true, Price: 3_000, Network: "sandbox", Scheme: "exact", Payable: true}},
		"a look-alike USDC":                          {[]string{probeOption("solana", "exact", fakeUSDC, 1_000)}, "", ProbeWant{Detail: "no USDC-on-Solana option"}},
		"another chain only":                         {[]string{probeOption("base", "exact", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", 1_000)}, "", ProbeWant{Detail: "no USDC-on-Solana option"}},
		"a scheme nobody pays":                       {[]string{probeOption("solana", "streaming", solUSDC, 1_000)}, "", ProbeWant{Detail: "no USDC-on-Solana option"}},
		"the sandbox when asked about solana":        {[]string{probeOption("sandbox", "exact", "USDC", 3_000)}, "solana", ProbeWant{Detail: "no USDC-on-Solana option"}},
	} {
		p := newProvider(t)
		p.challenge = func(w http.ResponseWriter, r *http.Request) { writeChallenge(w, tc.options...) }
		res, err := newRunner(0, 0).Probe(context.Background(), probeCandidate(t, p, tc.network), nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := ProbeWant{Priced: res.Priced, Price: res.PriceMinor, Network: res.Network, Scheme: res.Scheme, Payable: res.Payable, Detail: res.Detail}
		if tc.want.Detail != "" {
			if got.Priced || !strings.Contains(got.Detail, tc.want.Detail) {
				t.Errorf("%s: want unpriced with %q, got %+v", name, tc.want.Detail, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: want %+v, got %+v", name, tc.want, got)
		}
	}
}

// ProbeWant is what a probe is expected to find.
type ProbeWant struct {
	Priced  bool
	Price   int64
	Network string
	Scheme  string
	Payable bool
	Detail  string
}

func TestProbeSaysWhenAProviderIsNotAnX402Resource(t *testing.T) {
	p := newProvider(t)
	p.challenge = func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusNotFound) }
	res, err := newRunner(0, 0).Probe(context.Background(), probeCandidate(t, p, ""), nil)
	if err != nil || res.Status != 404 || res.Priced {
		t.Errorf("%+v %v", res, err)
	}
	// And a network the server has no rail for is priced but not payable.
	p = newProvider(t)
	p.challenge = func(w http.ResponseWriter, r *http.Request) {
		writeChallenge(w, probeOption("solana-devnet", "exact", "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", 1_000))
	}
	r := New(Config{HTTP: newRunner(0, 0).http, Networks: map[string]string{"solana": "x402-solana"}})
	res, err = r.Probe(context.Background(), probeCandidate(t, p, ""), nil)
	if err != nil || !res.Priced || res.Payable || res.Network != "solana-devnet" {
		t.Errorf("devnet without a devnet rail: %+v %v", res, err)
	}
}
