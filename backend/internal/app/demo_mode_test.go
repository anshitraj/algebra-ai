package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/connectors/democheckout"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
	"github.com/project-algebra/algebra/policy"
)

type fakeModes map[string]account.Mode

func (f fakeModes) UserMode(_ context.Context, userID string) (account.Mode, error) {
	if m, ok := f[userID]; ok {
		return m, nil
	}
	return account.ModeLive, shared.ErrNotFound
}

type staticWebSearcher struct{ results []websearch.Result }

func (f staticWebSearcher) Search(context.Context, string, int) ([]websearch.Result, error) {
	return f.results, nil
}

var mouseListings = []websearch.Result{
	{Title: "HP X200 Wireless Mouse", Store: "Amazon", URL: "https://www.amazon.in/dp/B0HPX200", PriceMinorUnits: 54900, Currency: "INR"},
	{Title: "Logitech M331 Silent Plus Wireless Mouse", Store: "Amazon", URL: "https://www.amazon.in/dp/B0M331", PriceMinorUnits: 119500, Currency: "INR"},
}

// demoHarness is the orchestration harness wired the way production wires
// demo accounts: the demo checkout registered, fed by the web search, and
// routing by account mode on.
func demoHarness(t *testing.T) (*harness, fakeModes) {
	t.Helper()
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.Discovery.SetWebSearcher(staticWebSearcher{results: mouseListings})
	demo := democheckout.New(h.Discovery.WebListings)
	h.connectors.Register(demo)
	h.Discovery.SetListingObserver(demo.Remember)
	modes := fakeModes{"demo-user": account.ModeDemo, "live-user": account.ModeLive}
	h.Discovery.SetAccountModes(modes)
	return h, modes
}

func TestDemoMode_BuysTheListingTheUserWasShown(t *testing.T) {
	h, _ := demoHarness(t)
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "demo-user", agent.AllPermissions...)
	h.seedShippingProfile(t, "demo-user", "shipping:home", testShippingProfile())

	// The agent shows the user real listings first…
	if _, err := h.Discovery.SearchWebWithin(ctx, agentID, "wireless mouse", 5, 0); err != nil {
		t.Fatal(err)
	}
	// …then buys the one they picked.
	pi, err := h.IntentSvc.CreateIntent(ctx, h.idempotency, "", CreateIntentInput{
		UserID: "demo-user", AgentID: agentID,
		Items: []intent.Item{{Query: "HP X200 Wireless Mouse", Quantity: 1}},
		Constraints: intent.Constraints{
			MaxTotalMinorUnits: 100000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home",
			PreferredMerchants: []string{"zepto", "amazon"}, // a demo account ignores these
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	quotes, err := h.Discovery.Discover(ctx, agentID, pi.ID)
	if err != nil || len(quotes) != 1 {
		t.Fatalf("Discover: %v, quotes=%+v", err, quotes)
	}
	q := quotes[0]
	if q.Merchant != democheckout.Name || q.Subtotal.MinorUnits != 54900 || q.FinalPayable.MinorUnits != 54900 {
		t.Fatalf("expected the ₹549 Amazon listing via demo checkout with free delivery, got %+v", q)
	}
	if !strings.Contains(q.Items[0].Name, "HP X200") || !strings.Contains(q.Items[0].Name, "Amazon") {
		t.Fatalf("quote should name the product and the store it's listed on: %q", q.Items[0].Name)
	}
	if err := h.QuoteSvc.SelectQuote(ctx, agentID, pi.ID, q.QuoteID); err != nil {
		t.Fatal(err)
	}
	dec, err := h.PolicySvc.EvaluateAndTransition(ctx, agentID, pi.ID)
	if err != nil || dec.Decision != policy.Allow {
		t.Fatalf("policy: %v, %+v", err, dec)
	}
	outcome, err := h.Orders.Execute(ctx, h.idempotency, "exec-demo", agentID, pi.ID)
	if err != nil || outcome.IntentStatus != intent.StateSucceeded {
		t.Fatalf("Execute: %v, %+v", err, outcome)
	}
	ord := outcome.Order
	if ord.ProviderMode != "mock" || !strings.HasPrefix(ord.MerchantOrderID, "DEMO-") || ord.DeliveryETA == nil {
		t.Fatalf("a demo order must be clearly simulated: %+v", ord)
	}

	detail, err := h.Orders.OrderForUser(ctx, "demo-user", ord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Simulated || detail.ShipTo == nil || detail.ShipTo.City != "Bengaluru" || len(detail.Events) == 0 {
		t.Fatalf("order detail incomplete: %+v", detail)
	}
	if _, err := h.Orders.OrderForUser(ctx, "someone-else", ord.ID); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("another user must not see this order, got %v", err)
	}
}

func TestLiveMode_NeverReachesDemoCheckoutOrMockStore(t *testing.T) {
	h, _ := demoHarness(t)
	ctx := context.Background()
	agentID := h.mustCreateAgent(t, "live-user", agent.AllPermissions...)

	if got := h.Discovery.candidateMerchants(&intent.PurchaseIntent{}, account.ModeLive); len(got) != 0 {
		t.Fatalf("a live account has no real checkout store here, so no candidates; got %v", connectorNames(got))
	}
	results, err := h.Discovery.SearchProducts(ctx, agentID, "chips", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Merchant == "mock" || r.Merchant == democheckout.Name {
			t.Fatalf("live search must not include %s", r.Merchant)
		}
	}
}

func TestDemoMode_BrowsingSkipsTestStores(t *testing.T) {
	h, _ := demoHarness(t)
	agentID := h.mustCreateAgent(t, "demo-user", agent.PermShoppingRead)
	results, err := h.Discovery.SearchProducts(context.Background(), agentID, "chips", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Merchant == "mock" || r.Merchant == democheckout.Name {
			t.Fatalf("demo browsing shows real stores only (web_search covers demo checkout), got %s", r.Merchant)
		}
	}
	got := connectorNames(h.Discovery.candidateMerchants(&intent.PurchaseIntent{}, account.ModeDemo))
	if len(got) != 1 || !got[democheckout.Name] {
		t.Fatalf("a demo account buys only through the demo checkout, got %v", got)
	}
}
