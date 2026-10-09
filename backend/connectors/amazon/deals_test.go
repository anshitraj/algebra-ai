package amazon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Field names and shapes follow the Creators API OffersV2 reference:
// price.savings{money, percentage}, price.savingBasis{money,
// savingBasisType, savingBasisTypeLabel}, dealDetails{accessType, badge,
// earlyAccessDurationInMilliseconds, startTime, endTime, percentClaimed} —
// including its seconds-less timestamps and string percentClaimed.
const dealsReply = `{"searchResult":{"items":[
 {"asin":"B0MOUSE001","detailPageURL":"https://www.amazon.in/dp/B0MOUSE001?tag=tag-21",
  "itemInfo":{"title":{"displayValue":"Logitech M331 Silent Plus"},"byLineInfo":{"brand":{"displayValue":"Logitech"}}},
  "offersV2":{"listings":[{"price":{"money":{"amount":1195.0,"currency":"INR"},
     "savings":{"money":{"amount":600.0,"currency":"INR"},"percentage":33},
     "savingBasis":{"money":{"amount":1795.0,"currency":"INR"},"savingBasisType":"LIST_PRICE","savingBasisTypeLabel":"M.R.P.:"}},
   "availability":{"type":"IN_STOCK"},
   "dealDetails":{"accessType":"ALL","badge":"Limited time deal","startTime":"2026-09-24T00:00Z","endTime":"2026-09-25T18:30Z","percentClaimed":"42"}}]}},
 {"asin":"B0MOUSE002","detailPageURL":"https://www.amazon.in/dp/B0MOUSE002",
  "itemInfo":{"title":{"displayValue":"Full-price mouse"}},
  "offersV2":{"listings":[{"price":{"money":{"amount":999.0,"currency":"INR"}},"availability":{"type":"IN_STOCK"}}]}},
 {"asin":"B0MOUSE003","detailPageURL":"https://www.amazon.in/dp/B0MOUSE003",
  "itemInfo":{"title":{"displayValue":"Prime early-access mouse"}},
  "offersV2":{"listings":[{"price":{"money":{"amount":2495.0,"currency":"INR"}},"availability":{"type":"IN_STOCK"},
   "dealDetails":{"accessType":"PRIME_EARLY_ACCESS","badge":"Ends in ","earlyAccessDurationInMilliseconds":86400000,"startTime":"2026-09-24T06:00:00Z","endTime":"2026-09-26T06:00:00Z","percentClaimed":7}}]}},
 {"asin":"B0MOUSE004","detailPageURL":"https://www.amazon.in/dp/B0MOUSE004",
  "itemInfo":{"title":{"displayValue":"Expired deal mouse"}},
  "offersV2":{"listings":[{"price":{"money":{"amount":799.0,"currency":"INR"}},"availability":{"type":"IN_STOCK"},
   "dealDetails":{"accessType":"ALL","badge":"Deal","startTime":"2026-09-01T00:00Z","endTime":"2026-09-02T00:00Z"}}]}},
 {"asin":"B0MOUSE005","detailPageURL":"https://www.amazon.in/dp/B0MOUSE005",
  "itemInfo":{"title":{"displayValue":"Out of stock discounted mouse"}},
  "offersV2":{"listings":[{"price":{"money":{"amount":499.0,"currency":"INR"},"savings":{"money":{"amount":500.0,"currency":"INR"},"percentage":50}},"availability":{"type":"OUT_OF_STOCK"}}]}}
]}}`

func TestFindDeals_SavingsAndLiveDeals(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	f.reply = dealsReply
	c := f.connector("3.2")
	c.now = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }

	deals, err := c.FindDeals(context.Background(), "silent mouse", 5)
	if err != nil {
		t.Fatalf("FindDeals: %v", err)
	}
	f.mu.Lock()
	body := f.lastBody
	f.mu.Unlock()
	resources, _ := body["resources"].([]any)
	if !slices.Contains(resources, any("offersV2.listings.dealDetails")) || !slices.Contains(resources, any("offersV2.listings.price")) {
		t.Fatalf("deal resources not requested: %v", resources)
	}

	if len(deals) != 2 {
		t.Fatalf("got %d deals, want 2 (discounted/live, in-stock only): %+v", len(deals), deals)
	}
	m := deals[0]
	if m.Kind != deal.KindItemDeal || m.Source != deal.SourceAmazonCreators || m.Title != "Logitech M331 Silent Plus" || m.Description != "Logitech" {
		t.Fatalf("unexpected deal %+v", m)
	}
	if m.PriceMinorUnits != 119500 || m.WasMinorUnits != 179500 || m.SavingsMinorUnits != 60000 || m.SavingsPercent != 33 || m.BasisLabel != "M.R.P." {
		t.Fatalf("savings not mapped: %+v", m)
	}
	if m.Badge != "Limited time deal" || m.PercentClaimed != 42 || m.PrimeOnly || m.EndsAt == nil || !m.EndsAt.Equal(time.Date(2026, 9, 25, 18, 30, 0, 0, time.UTC)) {
		t.Fatalf("deal details not mapped: %+v", m)
	}

	p := deals[1]
	if p.Title != "Prime early-access mouse" || !p.PrimeOnly || p.Badge != "" || p.SavingsMinorUnits != 0 || p.PercentClaimed != 7 {
		t.Fatalf("unexpected prime deal %+v (countdown badge must be dropped)", p)
	}

	// Once the early-access window has passed, everyone can buy it.
	c.now = func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }
	deals, err = c.FindDeals(context.Background(), "silent mouse", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range deals {
		if d.Title == "Prime early-access mouse" && d.PrimeOnly {
			t.Fatalf("early access should have ended: %+v", d)
		}
	}
}

func TestFindDeals_Unconfigured(t *testing.T) {
	if _, err := New(Config{}).FindDeals(context.Background(), "mouse", 5); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestSearchProductsUnchangedByRefactor(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	f.reply = dealsReply
	products, err := f.connector("3.2").SearchProducts(context.Background(), "mouse", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 3 || products[0].MerchantProductID != "B0MOUSE001" {
		t.Fatalf("unexpected products %+v", products)
	}
	f.mu.Lock()
	resources, _ := f.lastBody["resources"].([]any)
	f.mu.Unlock()
	if slices.Contains(resources, any("offersV2.listings.dealDetails")) {
		t.Fatal("plain search should not request deal details")
	}
}

func TestParseDealTime(t *testing.T) {
	for _, s := range []string{"2025-02-21T05:35Z", "2025-02-21T05:35:00Z", "2025-02-21T11:05+05:30"} {
		got := parseDealTime(s)
		if got == nil || !got.Equal(time.Date(2025, 2, 21, 5, 35, 0, 0, time.UTC)) {
			t.Errorf("parseDealTime(%q) = %v", s, got)
		}
	}
	if parseDealTime("soon") != nil || parseDealTime("") != nil {
		t.Error("unparseable times must be nil")
	}
}
