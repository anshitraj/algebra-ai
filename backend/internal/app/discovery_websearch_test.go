package app

import (
	"context"
	"errors"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
	"github.com/project-algebra/algebra/policy"
)

type fakeWebSearcher struct {
	results []websearch.Result
	err     error
	calls   int
	lastQ   string
	lastN   int
}

func (f *fakeWebSearcher) Search(_ context.Context, query string, limit int) ([]websearch.Result, error) {
	f.calls++
	f.lastQ, f.lastN = query, limit
	return f.results, f.err
}

func TestSearchWeb_OffByDefault(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)

	if _, err := h.Discovery.SearchWeb(context.Background(), agentID, "rare gadget", 5); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented when no web searcher is configured, got %v", err)
	}
}

func TestSearchWeb_UsesConfiguredSearcher(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	fake := &fakeWebSearcher{results: []websearch.Result{{Title: "t", Snippet: "s", URL: "https://example.com/x"}}}
	h.Discovery.SetWebSearcher(fake)
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)

	results, err := h.Discovery.SearchWeb(context.Background(), agentID, "rare gadget", 0)
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/x" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if fake.calls != 1 || fake.lastQ != "rare gadget" || fake.lastN != 5 {
		t.Fatalf("searcher called wrong: calls=%d q=%q n=%d", fake.calls, fake.lastQ, fake.lastN)
	}
}

func TestSearchWeb_RequiresPermission(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.Discovery.SetWebSearcher(&fakeWebSearcher{})
	agentID := h.mustCreateAgent(t, "user-1") // no PermShoppingRead

	if _, err := h.Discovery.SearchWeb(context.Background(), agentID, "x", 5); err == nil {
		t.Fatal("expected a permission error")
	}
}
