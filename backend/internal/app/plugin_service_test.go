package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/plugin"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
)

type memPluginStore struct {
	mu   sync.Mutex
	rows map[string]map[string]PluginChoice
}

func (m *memPluginStore) Choices(_ context.Context, userID string) (map[string]PluginChoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]PluginChoice{}
	for k, v := range m.rows[userID] {
		out[k] = v
	}
	return out, nil
}

func (m *memPluginStore) SetChoice(_ context.Context, userID, pluginID string, c PluginChoice, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[userID] == nil {
		m.rows[userID] = map[string]PluginChoice{}
	}
	m.rows[userID][pluginID] = c
	return nil
}

type recordingCommunity struct {
	sites []websearch.CommunitySite
	calls int
}

func (r *recordingCommunity) SearchCommunity(_ context.Context, _ string, sites []websearch.CommunitySite, _ int) ([]websearch.Tip, error) {
	r.calls++
	r.sites = sites
	return []websearch.Tip{{Title: "Whey 1kg ₹1,899", URL: "https://www.reddit.com/r/IndianFitness/comments/a/", Source: "r/IndianFitness", Code: "MB200"}}, nil
}

func pluginHarness(t *testing.T) (*DiscoveryService, *PluginService, *recordingCommunity) {
	t.Helper()
	agents := newFakeAgentStore()
	agents.put(&agentpkg.Identity{ID: "agent_1", UserID: "user_1", Permissions: []agentpkg.Permission{agentpkg.PermShoppingRead}})
	svc := NewDiscoveryService(newFakeIntentStore(), agents, newFakeQuoteStore(), NewConnectorRegistry(), newFakeAuditLogger(), time.Minute)
	plugins := NewPluginService(&memPluginStore{rows: map[string]map[string]PluginChoice{}}, agents, map[string]string{plugin.AmazonDeals: "Needs keys"})
	svc.SetPlugins(plugins)
	rec := &recordingCommunity{}
	svc.SetCommunitySearcher(rec)
	return svc, plugins, rec
}

func TestPlugins_CoreCantBeTurnedOffAndSettingsAreValidated(t *testing.T) {
	_, plugins, _ := pluginHarness(t)
	ctx := context.Background()
	if _, err := plugins.Set(ctx, "user_1", plugin.WebPrices, false, nil); !errors.Is(err, shared.ErrConflict) {
		t.Errorf("turning off a core plugin should conflict, got %v", err)
	}
	if _, err := plugins.Set(ctx, "user_1", "not_real", true, nil); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("unknown plugin should be not found, got %v", err)
	}
	if _, err := plugins.Set(ctx, "user_1", plugin.RedditDeals, true, &plugin.Config{Subreddits: []string{"a", "b", "c"}}); !errors.Is(err, shared.ErrConflict) {
		t.Errorf("three subreddits should be refused, got %v", err)
	}
	if _, err := plugins.Set(ctx, "user_1", plugin.DesiDimeDeals, true, &plugin.Config{Subreddits: []string{"x"}}); err == nil {
		t.Error("only the Reddit plugin takes subreddits")
	}
	list, _ := plugins.List(ctx, "user_1")
	for _, st := range list {
		if st.ID == plugin.AmazonDeals && (st.Ready || st.Detail == "") {
			t.Error("a plugin the server can't run shows as needing setup")
		}
	}
}

func TestCommunityTips_ReadsOnlyWhatThePersonTurnedOn(t *testing.T) {
	svc, plugins, rec := pluginHarness(t)
	ctx := context.Background()

	if _, err := svc.CommunityTips(ctx, "agent_1", "whey protein"); !errors.Is(err, shared.ErrUnauthorized) {
		t.Fatalf("no community plugin on: want refusal, got %v", err)
	}
	if rec.calls != 0 {
		t.Fatal("nothing may be searched while every community plugin is off")
	}

	if _, err := plugins.Set(ctx, "user_1", plugin.RedditDeals, true, &plugin.Config{Subreddits: []string{"r/IndianFitness"}}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.CommunityTips(ctx, "agent_1", "whey protein under 2000")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.sites) != 1 || rec.sites[0].Path != "/r/IndianFitness" {
		t.Errorf("should search only the chosen subreddit, got %+v", rec.sites)
	}
	if len(res.Searched) != 1 || res.Searched[0] != "r/IndianFitness" || len(res.Tips) != 1 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestFindDeals_PluginOffSkipsThatSource(t *testing.T) {
	svc, plugins, _ := pluginHarness(t)
	ctx := context.Background()
	if _, err := plugins.Set(ctx, "user_1", plugin.BankOffers, false, nil); err != nil {
		t.Fatal(err)
	}
	res, err := svc.FindDeals(ctx, "agent_1", DealQuery{Query: "mouse"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range res.Notes {
		if n.Detail == "The Bank & card offers plugin is off." {
			found = true
		}
	}
	if !found {
		t.Errorf("bank offers should be skipped with a note, got %+v", res.Notes)
	}
}
