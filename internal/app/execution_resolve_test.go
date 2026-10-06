package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// fakeCatalog is a CatalogSource with two providers and a counter, so a test
// can tell when the catalog was consulted.
type fakeCatalog struct {
	calls int
	err   error
	byCap map[string][]routing.Candidate // capability -> candidates; "paysh:<x>" providers are looked up inside
}

func (f *fakeCatalog) Owns(provider string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(provider)), "paysh:")
}

func (f *fakeCatalog) CandidatesFor(_ context.Context, provider, capability string) ([]routing.Candidate, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []routing.Candidate
	for _, c := range f.byCap[capability] {
		if c.Provider == strings.ToLower(provider) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeCatalog) ForCapability(_ context.Context, capability string) ([]routing.Candidate, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.byCap[capability], nil
}

func catalogCand(t *testing.T, capability, provider, endpoint string) routing.Candidate {
	t.Helper()
	c, err := routing.Candidate{
		Capability: capability, Provider: provider, ExecutionType: routing.ExecX402, Endpoint: endpoint, Method: "GET", Network: "solana",
		Sources: []routing.DiscoverySource{routing.SourcePaySh},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func providersOf(cs []routing.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Provider)
	}
	slices.Sort(out)
	return out
}

func TestCandidateResolver(t *testing.T) {
	ctx := context.Background()
	configured := map[string][]routing.Candidate{
		"acme": {configuredCand(t, "solana.token-risk", "acme", "https://api.acme.example/risk")},
	}
	cat := &fakeCatalog{byCap: map[string][]routing.Candidate{
		"birdeye.data.get.price": {catalogCand(t, "birdeye.data.get.price", "paysh:birdeye.data", "https://public-api.birdeye.so/price")},
	}}
	r := CandidateResolver{Configured: configured, Catalog: cat}

	t.Run("without a catalog it is ResolveCandidates", func(t *testing.T) {
		plain := CandidateResolver{Configured: configured}
		got, rej := plain.Resolve(ctx, "solana.token-risk", []string{"acme"}, nil)
		if !slices.Equal(providersOf(got), []string{"acme"}) || len(rej) != 0 {
			t.Errorf("%v %v", providersOf(got), rej)
		}
	})

	t.Run("a named catalog provider comes from the catalog and not from the configured set", func(t *testing.T) {
		cat.calls = 0
		got, rej := r.Resolve(ctx, "birdeye.data.get.price", []string{"PaySh:Birdeye.Data"}, nil)
		if !slices.Equal(providersOf(got), []string{"paysh:birdeye.data"}) || len(rej) != 0 || cat.calls != 1 {
			t.Errorf("%v %v calls=%d", providersOf(got), rej, cat.calls)
		}
		if got[0].Trust() != routing.TrustListed {
			t.Errorf("a catalog entry is listed, not native: %s", got[0].Trust())
		}
	})

	t.Run("naming only a catalog provider must not pull in every configured provider", func(t *testing.T) {
		got, _ := r.Resolve(ctx, "solana.token-risk", []string{"paysh:birdeye.data"}, nil)
		if len(got) != 0 {
			t.Errorf("the configured acme does solana.token-risk, but nobody asked for it: %v", providersOf(got))
		}
	})

	t.Run("a catalog provider that doesn't do the capability is rejected with a reason", func(t *testing.T) {
		got, rej := r.Resolve(ctx, "birdeye.data.get.nothing", []string{"paysh:birdeye.data"}, nil)
		if len(got) != 0 || len(rej) != 1 || rej[0].Provider != "paysh:birdeye.data" || !strings.Contains(rej[0].Detail, "birdeye.data.get.nothing") {
			t.Errorf("%v %+v", got, rej)
		}
	})

	t.Run("a catalog that can't be read is a rejection, and the configured ones still resolve", func(t *testing.T) {
		broken := &fakeCatalog{err: errors.New("pay.sh is down")}
		got, rej := CandidateResolver{Configured: configured, Catalog: broken}.Resolve(ctx, "solana.token-risk", []string{"acme", "paysh:birdeye.data"}, nil)
		if !slices.Equal(providersOf(got), []string{"acme"}) || len(rej) != 1 || !strings.Contains(rej[0].Detail, "pay.sh is down") {
			t.Errorf("%v %+v", providersOf(got), rej)
		}
	})

	t.Run("configured and catalog providers can be named together", func(t *testing.T) {
		both := map[string][]routing.Candidate{"acme": {configuredCand(t, "birdeye.data.get.price", "acme", "https://api.acme.example/price")}}
		got, rej := CandidateResolver{Configured: both, Catalog: cat}.Resolve(ctx, "birdeye.data.get.price", []string{"acme", "paysh:birdeye.data"}, nil)
		if !slices.Equal(providersOf(got), []string{"acme", "paysh:birdeye.data"}) || len(rej) != 0 {
			t.Errorf("%v %v", providersOf(got), rej)
		}
	})

	t.Run("a capability the operator configured never waits on the catalog", func(t *testing.T) {
		cat.calls = 0
		got, _ := r.Resolve(ctx, "solana.token-risk", nil, nil)
		if !slices.Equal(providersOf(got), []string{"acme"}) || cat.calls != 0 {
			t.Errorf("%v calls=%d", providersOf(got), cat.calls)
		}
	})

	t.Run("with nothing named and nothing configured, the capability says whose it is", func(t *testing.T) {
		cat.calls = 0
		got, rej := r.Resolve(ctx, "birdeye.data.get.price", nil, nil)
		if !slices.Equal(providersOf(got), []string{"paysh:birdeye.data"}) || len(rej) != 0 || cat.calls != 1 {
			t.Errorf("%v %v calls=%d", providersOf(got), rej, cat.calls)
		}
		if got, rej := r.Resolve(ctx, "nobody.knows.this", nil, nil); len(got) != 0 || len(rej) != 0 {
			t.Errorf("a capability that isn't the catalog's finds nothing and isn't an error: %v %v", got, rej)
		}
		down := CandidateResolver{Catalog: &fakeCatalog{err: errors.New("down")}}
		if got, rej := down.Resolve(ctx, "birdeye.data.get.price", nil, nil); len(got) != 0 || len(rej) != 1 {
			t.Errorf("an unreadable catalog is reported: %v %v", got, rej)
		}
	})

	t.Run("endpoints the agent supplies stay unverified and never consult the catalog", func(t *testing.T) {
		cat.calls = 0
		got, _ := r.Resolve(ctx, "birdeye.data.get.price", nil, []CandidateInput{{Endpoint: "https://x.example/p"}})
		if len(got) != 1 || got[0].Trust() != routing.TrustUnverified || cat.calls != 0 {
			t.Errorf("%+v calls=%d", got, cat.calls)
		}
	})
}
