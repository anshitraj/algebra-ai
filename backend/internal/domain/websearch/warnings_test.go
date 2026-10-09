package websearch

import (
	"slices"
	"testing"
)

func TestFlagSuspicious(t *testing.T) {
	results := FlagSuspicious([]Result{
		{Title: "Apple iPhone 15 (128GB)", Store: "Amazon", URL: "https://www.amazon.in/dp/x", PriceMinorUnits: 5990000},
		{Title: "Apple iPhone 15 (128GB)", Store: "Flipkart", URL: "https://www.flipkart.com/p/x", PriceMinorUnits: 5899900},
		{Title: "Apple iPhone 15 128GB MEGA SALE", Store: "mega-deals-store.shop", URL: "https://mega-deals-store.shop/iphone", PriceMinorUnits: 999900},
		{Title: "Apple iPhone 15 (128GB) refurbished", Store: "Croma", URL: "https://www.croma.com/x", PriceMinorUnits: 2500000},
		{Title: "No price shown", Store: "Somewhere", URL: "https://example.com/x"},
	})
	scam := results[2].Warnings
	if !slices.Contains(scam, WarnFarBelowOthers) || !slices.Contains(scam, WarnUnknownStore) {
		t.Fatalf("scam listing warnings = %v", scam)
	}
	// Far below, but a known retailer: flagged for price only.
	if w := results[3].Warnings; !slices.Equal(w, []string{WarnFarBelowOthers}) {
		t.Fatalf("known-store low price warnings = %v", w)
	}
	for _, i := range []int{0, 1, 4} {
		if len(results[i].Warnings) != 0 {
			t.Fatalf("result %d should not be flagged: %v", i, results[i].Warnings)
		}
	}
}

func TestFlagSuspicious_NeedsSomethingToCompare(t *testing.T) {
	one := FlagSuspicious([]Result{{Store: "tiny.shop", PriceMinorUnits: 100}, {Store: "x"}})
	if len(one[0].Warnings) != 0 {
		t.Fatal("a single priced listing has nothing to be far below")
	}
	// Similar prices from unknown stores are fine.
	similar := FlagSuspicious([]Result{
		{Title: "Boat Airdopes 141", Store: "a.shop", PriceMinorUnits: 100000},
		{Title: "Boat Airdopes 141", Store: "b.shop", PriceMinorUnits: 90000},
		{Title: "Boat Airdopes 141", Store: "c.shop", PriceMinorUnits: 110000},
	})
	for _, r := range similar {
		if len(r.Warnings) != 0 {
			t.Fatalf("unexpected warning %v", r.Warnings)
		}
	}
}

// A search mixes price tiers: a cheap mouse isn't suspicious next to a
// premium one — only next to the same product.
func TestFlagSuspicious_ComparesOnlyTheSameProduct(t *testing.T) {
	results := FlagSuspicious([]Result{
		{Title: "HP X200 Wireless Mouse", Store: "Amazon", PriceMinorUnits: 54900},
		{Title: "Logitech M331 Silent Plus Wireless Mouse", Store: "Amazon", PriceMinorUnits: 119500},
		{Title: "Logitech MX Master 3S Wireless Mouse", Store: "Amazon", PriceMinorUnits: 899500},
	})
	for _, r := range results {
		if len(r.Warnings) != 0 {
			t.Fatalf("%s flagged: %v", r.Title, r.Warnings)
		}
	}
}

func TestKnownStore(t *testing.T) {
	for _, r := range []Result{
		{URL: "https://www.flipkart.com/x"}, {URL: "https://blinkit.com/prn/x"}, {Store: "Swiggy Instamart"}, {Store: "Reliance Digital"},
	} {
		if !KnownStore(r) {
			t.Errorf("%+v should be known", r)
		}
	}
	for _, r := range []Result{{URL: "https://amazon.in.deals-now.shop/x", Store: "deals-now"}, {Store: "mega-deals-store.shop"}} {
		if KnownStore(r) {
			t.Errorf("%+v should not be known", r)
		}
	}
}

func TestFlagSuspicious_ComparesOnlyTheSamePackSize(t *testing.T) {
	rs := FlagSuspicious([]Result{
		{Title: "Coca-Cola Zero Sugar PET", Snippet: "750 ml", Store: "Zepto", PriceMinorUnits: 3600},
		{Title: "Coca-Cola Zero Soft drink Pet Bottle 750ml", Snippet: "750 ml x 3", Store: "Swiggy Instamart", PriceMinorUnits: 11700},
		{Title: "Coca-Cola Zero Sugar PET", Snippet: "750 ml x 3", Store: "Blinkit", PriceMinorUnits: 11900},
		{Title: "Coca-Cola Zero Sugar PET", Snippet: "750 ml", Store: "mega-deals.shop", URL: "https://mega-deals.shop/x", PriceMinorUnits: 100},
	})
	if len(rs[0].Warnings) != 0 {
		t.Errorf("a single bottle must not be flagged against three-packs: %v", rs[0].Warnings)
	}
	if len(rs[3].Warnings) == 0 {
		t.Error("a ₹1 single bottle is still far below the ₹36 one")
	}
}
