package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/project-algebra/algebra/connectors/amazon"
	"github.com/project-algebra/algebra/connectors/flipkart"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

type staticBankOffers struct {
	offers []deal.BankOffer
	err    error
}

func (s staticBankOffers) Offers(context.Context) ([]deal.BankOffer, error) { return s.offers, s.err }

// offersFlipkart is the real Flipkart connector against a local server
// answering the offers endpoints in the documented shape.
func offersFlipkart(t *testing.T) *flipkart.Connector {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/offers/v1/all/json":
			_, _ = w.Write([]byte(`{"allOffersList":[
			 {"title":"Mouse & Keyboards","description":"Min 40% Off","url":"https://dl.flipkart.com/dl/offers/mice","availability":"LIVE"},
			 {"title":"Mouse Pads","description":"From ₹99","url":"https://evil.example/phish","availability":"LIVE"}]}`))
		case "/offers/v1/dotd/json":
			_, _ = w.Write([]byte(`{"dotdList":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return flipkart.New(flipkart.Config{AffiliateID: "aff", AffiliateToken: "tok", OffersBaseURL: srv.URL + "/offers/v1"})
}

func TestFindDeals_StoreDealsBankOffersAndNotes(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	h.connectors.Register(offersFlipkart(t))
	h.connectors.Register(amazon.New(amazon.Config{})) // not configured
	h.Discovery.SetURLAllowlist(merchant.NewAllowedDomains("flipkart.com", "amazon.in"))
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	h.Discovery.now = func() time.Time { return now }
	ends := now.Add(7 * 24 * time.Hour)
	h.Discovery.SetBankOffers(staticBankOffers{offers: []deal.BankOffer{
		{Merchant: "flipkart", Bank: "Axis Bank", Title: "5% off Axis", DiscountPercent: 5, EndsAt: ends, TermsURL: "https://www.flipkart.com/axis"},
		{Merchant: "flipkart", Bank: "HDFC Bank", Title: "10% off HDFC", DiscountPercent: 10, MaxDiscountMinorUnits: 12500, EndsAt: ends, TermsURL: "https://www.flipkart.com/hdfc"},
		{Merchant: "flipkart", Bank: "ICICI Bank", Title: "Big-ticket ICICI", DiscountPercent: 10, MinOrderMinorUnits: 500000, EndsAt: ends, TermsURL: "https://www.flipkart.com/icici"},
		{Merchant: "flipkart", Bank: "SBI", Title: "Expired SBI", DiscountPercent: 10, EndsAt: now.Add(-time.Hour), TermsURL: "https://www.flipkart.com/sbi"},
		{Merchant: "amazon", Bank: "HDFC Bank", Title: "Amazon HDFC", DiscountPercent: 10, EndsAt: ends, TermsURL: "https://www.amazon.in/hdfc"},
	}})
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)

	res, err := h.Discovery.FindDeals(context.Background(), agentID, DealQuery{
		Query: "wireless mouse", Merchants: []string{"flipkart", "amazon"}, PriceMinorUnits: 150000, Banks: []string{"hdfc"},
	})
	if err != nil {
		t.Fatalf("FindDeals: %v", err)
	}

	var store, bank []deal.Deal
	for _, d := range res.Deals {
		if d.Kind == deal.KindBankOffer {
			bank = append(bank, d)
		} else {
			store = append(store, d)
		}
	}
	if len(store) != 2 || store[0].URL != "https://dl.flipkart.com/dl/offers/mice" || store[1].URL != "" {
		t.Fatalf("store offers: want both mouse offers with the off-allowlist link blanked, got %+v", store)
	}
	// ICICI needs a ₹5,000 order (price is ₹1,500); SBI has ended.
	if len(bank) != 3 {
		t.Fatalf("bank offers: got %+v", bank)
	}
	first := bank[0]
	if first.Merchant != "flipkart" && first.Merchant != "amazon" || first.Bank != "HDFC Bank" || !first.MatchesUserCard {
		t.Fatalf("the user's own card should come first, got %+v", first)
	}
	if first.EstimatedDiscountMinorUnits != 12500 && first.EstimatedDiscountMinorUnits != 15000 {
		t.Fatalf("estimate = %d", first.EstimatedDiscountMinorUnits)
	}
	if bank[2].Bank != "Axis Bank" || bank[2].MatchesUserCard || bank[2].EstimatedDiscountMinorUnits != 7500 {
		t.Fatalf("non-matching card should come last: %+v", bank[2])
	}

	var amazonNote bool
	for _, n := range res.Notes {
		if n.Merchant == "amazon" && n.Detail != "" {
			amazonNote = true
		}
	}
	if !amazonNote {
		t.Fatalf("an unconfigured Amazon should explain itself in a note: %+v", res.Notes)
	}
}

func TestFindDeals_UnconfiguredConnectorNeverTripsBreaker(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	unconfigured := flipkart.New(flipkart.Config{})
	h.connectors.Register(unconfigured)
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)
	for range 10 {
		res, err := h.Discovery.FindDeals(context.Background(), agentID, DealQuery{Query: "mouse", Merchants: []string{"flipkart"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Notes) == 0 || res.Notes[0].Merchant != "flipkart" {
			t.Fatalf("expected a flipkart note, got %+v", res.Notes)
		}
	}
}

func TestFindDeals_NoBankOfferSourceSaysSo(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	agentID := h.mustCreateAgent(t, "user-1", agent.PermShoppingRead)
	res, err := h.Discovery.FindDeals(context.Background(), agentID, DealQuery{Query: "mouse"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range res.Notes {
		if n.Merchant == "" && n.Detail != "" {
			found = true
		}
	}
	if !found || res.Deals == nil {
		t.Fatalf("expected a bank-offers note and a non-nil deals list, got %+v", res)
	}
}

func TestFindDeals_RequiresShoppingRead(t *testing.T) {
	h := newHarness(policy.NewLocalProvider(policy.DefaultRules()))
	agentID := h.mustCreateAgent(t, "user-1")
	if _, err := h.Discovery.FindDeals(context.Background(), agentID, DealQuery{Query: "mouse"}); !errors.Is(err, shared.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestDealErrorDetail(t *testing.T) {
	err := errors.Join(errors.New("x"))
	if got := dealErrorDetail(err); got != "Couldn't read this store's deals right now." {
		t.Fatalf("generic error leaked detail: %q", got)
	}
	wrapped := flipkart.New(flipkart.Config{})
	_, err = wrapped.FindDeals(context.Background(), "mouse", 5)
	if got := dealErrorDetail(err); got == "" || got[:3] != "Set" {
		t.Fatalf("not-configured detail = %q", got)
	}
}
