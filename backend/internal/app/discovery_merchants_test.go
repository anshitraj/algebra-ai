package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/project-algebra/algebra/connectors/blinkit"
	"github.com/project-algebra/algebra/connectors/flipkart"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/policy"
)

// searchOnlyFlipkart is the real Flipkart connector pointed at a local
// server answering in the Affiliate API's documented shape: it can search,
// but has no cart.
func searchOnlyFlipkart(t *testing.T) *flipkart.Connector {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"productInfoList":[{"productBaseInfoV1":{"productId":"CHPS1","title":"Lay's Classic Salted Chips","productBrand":"Lay's","inStock":true,"productUrl":"https://www.flipkart.com/lays/p/itm1","flipkartSellingPrice":{"amount":20,"currency":"INR"}}}]}`))
	}))
	t.Cleanup(srv.Close)
	return flipkart.New(flipkart.Config{AffiliateID: "aff", AffiliateToken: "token", BaseURL: srv.URL})
}

func connectorNames(cs []merchant.Connector) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		out[c.Name()] = true
	}
	return out
}

// A search-only merchant must never be sent through cart creation: it would
// fail on every intent and trip its circuit breaker, which would then also
// hide its search results.
func TestDiscovery_OnlySearchAndCartMerchantsProduceQuotes(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.connectors.Register(searchOnlyFlipkart(t))
	h.connectors.Register(blinkit.New())

	got := connectorNames(h.Discovery.candidateMerchants(&intent.PurchaseIntent{}, ""))
	if len(got) != 1 || !got["mock"] {
		t.Fatalf("only merchants with search and cart may quote; candidates were %v", got)
	}

	pi := &intent.PurchaseIntent{}
	pi.Constraints.PreferredMerchants = []string{"flipkart"}
	if got := h.Discovery.candidateMerchants(pi, ""); len(got) != 0 {
		t.Fatalf("preferring a search-only merchant must not make it quote, got %v", connectorNames(got))
	}
}

func TestSearchProducts_CatalogResultsAndHandoffLinks(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.connectors.Register(searchOnlyFlipkart(t))
	h.connectors.Register(blinkit.New())
	h.Discovery.SetURLAllowlist(merchant.NewAllowedDomains("flipkart.com", "blinkit.com"))
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)

	results, err := h.Discovery.SearchProducts(context.Background(), agentID, "chips", 5)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	byMerchant := map[string]MerchantSearchResult{}
	for _, r := range results {
		byMerchant[r.Merchant] = r
	}
	if fk := byMerchant["flipkart"]; len(fk.Products) != 1 || fk.Products[0].URL != "https://www.flipkart.com/lays/p/itm1" || fk.HandoffURL != "" {
		t.Fatalf("flipkart should return real catalog results: %+v", fk)
	}
	if bl, ok := byMerchant["blinkit"]; !ok || bl.HandoffURL != "https://blinkit.com/s/?q=chips" || len(bl.Products) != 0 {
		t.Fatalf("blinkit should appear as a handoff link only: %+v", bl)
	}
	if len(byMerchant["mock"].Products) == 0 {
		t.Fatal("mock search results missing")
	}
}

func TestSearchProducts_HandoffLinkMustPassAllowlist(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.connectors.Register(blinkit.New())
	h.Discovery.SetURLAllowlist(merchant.NewAllowedDomains("flipkart.com"))
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)

	results, err := h.Discovery.SearchProducts(context.Background(), agentID, "chips", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Merchant == "blinkit" {
			t.Fatalf("a handoff link off the allowlist must be dropped: %+v", r)
		}
	}
}

func TestDescribeMerchants_SortedWithStatus(t *testing.T) {
	registry := NewConnectorRegistry()
	registry.Register(blinkit.New())
	registry.Register(searchOnlyFlipkart(t))

	infos := DescribeMerchants(registry)
	if len(infos) != 2 || infos[0].Name != "blinkit" || infos[1].Name != "flipkart" {
		t.Fatalf("expected merchants sorted by name, got %+v", infos)
	}
	if infos[0].Status == nil || infos[0].Status.Integration != merchant.IntegrationDeepLinkHandoff {
		t.Fatalf("blinkit status missing: %+v", infos[0])
	}
	if !infos[1].Capabilities.Search || infos[1].Capabilities.Cart {
		t.Fatalf("flipkart capabilities wrong: %+v", infos[1].Capabilities)
	}
}
