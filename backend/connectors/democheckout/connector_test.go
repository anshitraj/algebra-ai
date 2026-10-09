package democheckout

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
)

var listings = []websearch.Result{
	{Title: "Logitech M331 Silent Plus Wireless Mouse", Store: "Amazon", URL: "https://www.amazon.in/dp/B0M331", PriceMinorUnits: 119500, Currency: "INR"},
	{Title: "Amul Taaza Toned Milk 500 ml", Store: "Blinkit", URL: "https://blinkit.com/prn/amul-taaza", PriceMinorUnits: 2800, Currency: "INR"},
	{Title: "Unpriced listing", Store: "Somewhere", URL: "https://example.com/x"},
	{Title: "Imported gadget", Store: "US store", URL: "https://example.com/y", PriceMinorUnits: 999, Currency: "USD"},
}

func countingSearch(n *int32) SearchFunc {
	return func(context.Context, string, int) ([]websearch.Result, error) {
		atomic.AddInt32(n, 1)
		return listings, nil
	}
}

func TestSearchProducts_RecallsTheListingTheUserSaw(t *testing.T) {
	var searches int32
	c := New(countingSearch(&searches))
	c.Remember(listings)

	products, err := c.SearchProducts(context.Background(), "logitech m331 silent plus", 5)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&searches) != 0 {
		t.Fatal("a close match to a remembered listing must not search again")
	}
	if len(products) != 1 || products[0].PriceMinorUnits != 119500 || products[0].Brand != "Amazon" || products[0].Confidence < 0.5 {
		t.Fatalf("unexpected products %+v", products)
	}
}

// Found live, in the browser: the agent picked "Logitech M171 Wireless
// Optical Mouse" and a 60% word match recalled a different brand's wireless
// optical mouse instead. Brand and model number must match.
func TestRecall_NeverSubstitutesAnotherProduct(t *testing.T) {
	var searches int32
	c := New(countingSearch(&searches))
	c.Remember([]websearch.Result{
		{Title: "EVM EWLM-360 Wireless Ambidextrous Optical Mouse", Store: "Flipkart", URL: "https://www.flipkart.com/evm", PriceMinorUnits: 45900, Currency: "INR"},
		{Title: "Logitech M331 Silent Plus Wireless Mouse", Store: "Amazon", URL: "https://www.amazon.in/m331", PriceMinorUnits: 119500, Currency: "INR"},
	})
	for _, q := range []string{"Logitech M171 Wireless Optical Mouse", "HP wireless optical mouse", "Logitech M331 Silent Plus Wireless Mouse Black 2024"} {
		if hits := c.recall(q, 5); len(hits) != 0 {
			t.Errorf("recall(%q) = %+v, want no match", q, hits)
		}
	}
	if hits := c.recall("Logitech M331 Silent Plus Wireless Mouse", 5); len(hits) != 1 || hits[0].PriceMinorUnits != 119500 {
		t.Fatalf("the exact listing must still be recalled, got %+v", hits)
	}
}

func TestSearchProducts_SearchesWhenNothingMatches(t *testing.T) {
	var searches int32
	c := New(countingSearch(&searches))
	products, err := c.SearchProducts(context.Background(), "toned milk", 5)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&searches) != 1 {
		t.Fatalf("expected one live search, got %d", searches)
	}
	// Only a priced listing of the product asked for comes back — not the
	// mouse the same search also returned.
	if len(products) != 1 || products[0].Brand != "Blinkit" {
		t.Fatalf("expected just the milk listing, got %+v", products)
	}
	// And they're remembered for the purchase that follows.
	if _, err := c.SearchProducts(context.Background(), "amul taaza toned milk", 5); err != nil || atomic.LoadInt32(&searches) != 1 {
		t.Fatalf("expected a recall, not another search (%d searches, %v)", searches, err)
	}
}

func TestCheckout_QuoteAndOrder(t *testing.T) {
	c := New(countingSearch(new(int32)))
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.Remember(listings)
	ctx := context.Background()

	cases := []struct {
		query   string
		qty     int
		wantFee int64
		wantETA time.Duration
	}{
		{"logitech m331 silent plus", 1, 0, 48 * time.Hour},      // ₹1,195 ≥ ₹499: free, 2 days
		{"amul taaza toned milk 500", 2, 3000, 25 * time.Minute}, // quick commerce under ₹199: ₹30, 25 min
		{"amul taaza toned milk 500", 8, 0, 25 * time.Minute},    // ₹224 ≥ ₹199: free
	}
	for _, tc := range cases {
		products, err := c.SearchProducts(ctx, tc.query, 1)
		if err != nil || len(products) == 0 {
			t.Fatalf("%s: %v", tc.query, err)
		}
		cart, _ := c.CreateCart(ctx, "user")
		if _, err := c.AddToCart(ctx, cart.ID, products[0].MerchantProductID, tc.qty); err != nil {
			t.Fatal(err)
		}
		q, err := c.GetCheckoutQuote(ctx, cart.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := products[0].PriceMinorUnits*int64(tc.qty) + tc.wantFee
		if q.DeliveryFee.MinorUnits != tc.wantFee || q.FinalPayable.MinorUnits != want || !q.DeliveryETA.Equal(now.Add(tc.wantETA)) {
			t.Fatalf("%s ×%d: fee %d total %d eta %v", tc.query, tc.qty, q.DeliveryFee.MinorUnits, q.FinalPayable.MinorUnits, q.DeliveryETA)
		}
	}

	products, _ := c.SearchProducts(ctx, "logitech m331 silent plus", 1)
	cart, _ := c.CreateCart(ctx, "user")
	_, _ = c.AddToCart(ctx, cart.ID, products[0].MerchantProductID, 1)
	res, err := c.ExecuteCheckout(ctx, cart.ID, "appr", merchant.Fulfillment{})
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded {
		t.Fatalf("no address must stop the order: %+v, %v", res, err)
	}
	res, err = c.ExecuteCheckout(ctx, cart.ID, "appr", merchant.Fulfillment{Shipping: &merchant.ShippingAddress{City: "Bengaluru"}})
	if err != nil || res.Status != merchant.ExecutionSucceeded {
		t.Fatalf("checkout: %+v, %v", res, err)
	}
	if o := res.Order; !strings.HasPrefix(o.MerchantOrderID, "DEMO-") || len(o.MerchantOrderID) != 13 || o.Total.MinorUnits != 119500 || o.DeliveryETA == nil {
		t.Fatalf("unexpected order %+v", o)
	}
	if c.Mode() != merchant.ProviderModeMock {
		t.Fatal("demo orders must be provider_mode=mock")
	}
}

func TestNotReadyWithoutWebSearch(t *testing.T) {
	c := New(nil)
	if c.Capabilities().Cart || c.Status().Ready {
		t.Fatal("without web search the demo checkout can't price anything")
	}
	if _, err := c.SearchProducts(context.Background(), "mouse", 5); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestRecall_StoreNamedInQueryPicksThatStoresListing(t *testing.T) {
	c := New(nil)
	c.Remember([]websearch.Result{
		{Title: "Coca-Cola Zero Sugar PET", Store: "Zepto", URL: "https://www.zepto.com/pn/coke-zero/pvid/1", PriceMinorUnits: 3600, Currency: "INR"},
		{Title: "Coca-Cola Zero Sugar PET", Store: "Swiggy Instamart", URL: "https://www.swiggy.com/instamart/item/2", PriceMinorUnits: 3900, Currency: "INR"},
	})
	got := c.recall("Coca-Cola Zero Sugar PET from Zepto", 5)
	if len(got) != 1 || got[0].PriceMinorUnits != 3600 {
		t.Fatalf("want only the Zepto listing, got %+v", got)
	}
	if got := c.recall("Coca-Cola Zero Sugar PET from Blinkit", 5); len(got) != 0 {
		// Blinkit isn't a remembered store, so "from Blinkit" stays part of
		// the product words and nothing matches — never another store's listing.
		t.Fatalf("a store the user didn't pick must not be substituted: %+v", got)
	}
}
