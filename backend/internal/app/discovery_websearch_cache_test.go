package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/websearch"
)

type countingSearcher struct {
	calls   int
	results []websearch.Result
	err     error
}

func (c *countingSearcher) Search(context.Context, string, int) ([]websearch.Result, error) {
	c.calls++
	return c.results, c.err
}

type memCache struct {
	mu   sync.Mutex
	data map[string][]websearch.Result
}

func (m *memCache) GetJSON(_ context.Context, key string, out any) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return false
	}
	*(out.(*[]websearch.Result)) = v
	return true
}

func (m *memCache) SetJSON(_ context.Context, key string, v any, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = v.([]websearch.Result)
}

func newSearchHarness(t *testing.T, ws WebSearcher) (*DiscoveryService, *memCache) {
	t.Helper()
	agents := newFakeAgentStore()
	agents.put(&agentpkg.Identity{ID: "agent_1", UserID: "user_1", Permissions: []agentpkg.Permission{agentpkg.PermShoppingRead}})
	svc := NewDiscoveryService(newFakeIntentStore(), agents, newFakeQuoteStore(), NewConnectorRegistry(), newFakeAuditLogger(), time.Minute)
	svc.SetWebSearcher(ws)
	cache := &memCache{data: map[string][]websearch.Result{}}
	svc.SetSearchCache(cache, time.Minute)
	return svc, cache
}

func TestSearchWeb_CachesAcrossEquivalentQueries(t *testing.T) {
	ctx := context.Background()
	ws := &countingSearcher{results: []websearch.Result{{Title: "Coke Zero 750ml", URL: "https://blinkit.com/x", Store: "Blinkit"}}}
	svc, _ := newSearchHarness(t, ws)

	for _, q := range []string{"Coke Zero", "coke zero", "  Coke   Zero "} {
		got, err := svc.SearchWeb(ctx, "agent_1", q, 5)
		if err != nil || len(got) != 1 || got[0].Store != "Blinkit" {
			t.Fatalf("%q: %v %+v", q, err, got)
		}
	}
	if ws.calls != 1 {
		t.Errorf("equivalent queries should hit the provider once, got %d calls", ws.calls)
	}
	// A different limit is a different answer, so it must not reuse the cache.
	if _, err := svc.SearchWeb(ctx, "agent_1", "Coke Zero", 8); err != nil {
		t.Fatal(err)
	}
	if ws.calls != 2 {
		t.Errorf("a different limit should search again, got %d calls", ws.calls)
	}
}

func TestSearchWeb_DoesNotCacheEmptyOrFailedSearches(t *testing.T) {
	ctx := context.Background()
	ws := &countingSearcher{results: nil}
	svc, _ := newSearchHarness(t, ws)
	for i := 0; i < 2; i++ {
		if _, err := svc.SearchWeb(ctx, "agent_1", "unobtainium", 5); err != nil {
			t.Fatal(err)
		}
	}
	if ws.calls != 2 {
		t.Errorf("an empty result must not be cached (upstream hiccups hide products), got %d calls", ws.calls)
	}

	failing := &countingSearcher{err: errors.New("rate limited")}
	svc2, cache := newSearchHarness(t, failing)
	if _, err := svc2.SearchWeb(ctx, "coke", "x", 5); err == nil {
		t.Error("permission check should reject an unknown agent")
	}
	if _, err := svc2.SearchWeb(ctx, "agent_1", "coke", 5); err == nil {
		t.Error("a provider error must surface, not be swallowed")
	}
	if len(cache.data) != 0 {
		t.Error("a failed search must not be cached")
	}
}

type budgetSearcher struct {
	countingSearcher
	gotMax int64
}

func (b *budgetSearcher) SearchWithBudget(_ context.Context, _ string, _ int, maxPriceMinor int64) ([]websearch.Result, error) {
	b.calls++
	b.gotMax = maxPriceMinor
	return b.results, b.err
}

func TestSearchWebWithin_DropsListingsOverBudget(t *testing.T) {
	ctx := context.Background()
	ws := &budgetSearcher{countingSearcher: countingSearcher{results: []websearch.Result{
		{Title: "Ferrero Rocher 24pc", Store: "Blinkit", PriceMinorUnits: 89600},
		{Title: "Store page, no price", Store: "Zepto"},
		{Title: "Dairy Milk Silk", Store: "Zepto", PriceMinorUnits: 20000},
		{Title: "Ferrero Rocher 16pc", Store: "Amazon", PriceMinorUnits: 50000},
	}}}
	svc, _ := newSearchHarness(t, ws)

	got, err := svc.SearchWebWithin(ctx, "agent_1", "birthday chocolates", 8, 50000)
	if err != nil {
		t.Fatal(err)
	}
	if ws.gotMax != 50000 {
		t.Errorf("the budget should reach a budget-aware searcher, got %d", ws.gotMax)
	}
	want := []string{"Dairy Milk Silk", "Ferrero Rocher 16pc", "Store page, no price"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Title != w {
			t.Errorf("result %d = %q, want %q (priced within budget first, unpriced last)", i, got[i].Title, w)
		}
	}

	// Same query without a ceiling is a different answer — no cache reuse.
	all, err := svc.SearchWebWithin(ctx, "agent_1", "birthday chocolates", 8, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("no ceiling should return everything: %v %+v", err, all)
	}
	if ws.calls != 2 {
		t.Errorf("a different budget should search again, got %d calls", ws.calls)
	}
}
