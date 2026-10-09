package flipkart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/deal"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Shapes follow the Offer APIs reference (af_offer_ref.html): allOffersList
// with startTime/endTime in Unix milliseconds, dotdList without them.
const allOffersJSON = `{"allOffersList":[
 {"title":"Winter Jackets","description":"Min 50% Off on Men's Jackets","url":"https://dl.flipkart.com/dl/offers/jackets?affid=test","category":"Men's Clothing",
  "startTime":1790000000000,"endTime":1800000000000,"availability":"LIVE",
  "imageUrls":[{"url":"https://rukminim1.flixcart.com/low.jpg","resolutionType":"low"},{"url":"https://rukminim1.flixcart.com/mid.jpg","resolutionType":"mid"}]},
 {"title":"Bedsheets","description":"Flat 60% Off","url":"https://dl.flipkart.com/dl/offers/bedsheets","category":"Home Furnishing",
  "startTime":"1790000000000","endTime":"1800000000000","availability":"LIVE","imageUrls":[]},
 {"title":"Expired Jackets","description":"Ended last week","url":"https://dl.flipkart.com/dl/offers/old","category":"Men's Clothing",
  "startTime":1700000000000,"endTime":1710000000000,"availability":"LIVE"},
 {"title":"Sold-out Jackets","description":"Out of stock","url":"https://dl.flipkart.com/dl/offers/oos","category":"Men's Clothing","availability":"OOS"}
]}`

const dotdJSON = `{"dotdList":[
 {"title":"Puffer Jackets","description":"From ₹999","url":"https://dl.flipkart.com/dl/dotd/puffer","availability":"LIVE",
  "imageUrls":[{"url":"http://insecure.example/img.jpg","resolutionType":"mid"},{"url":"https://rukminim1.flixcart.com/dotd.jpg","resolutionType":"default"}]}
]}`

func newOffersServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.Header.Get("Fk-Affiliate-Id") != "aff" || r.Header.Get("Fk-Affiliate-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/offers/v1/all/json":
			_, _ = w.Write([]byte(allOffersJSON))
		case "/offers/v1/dotd/json":
			_, _ = w.Write([]byte(dotdJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newOffersConnector(srv *httptest.Server, now time.Time) *Connector {
	c := New(Config{AffiliateID: "aff", AffiliateToken: "tok", BaseURL: srv.URL + "/1.0", OffersBaseURL: srv.URL + "/offers/v1"})
	c.now = func() time.Time { return now }
	return c
}

// 2026-09-24, inside the live offers' window (1790000000000–1800000000000 ms).
var feedNow = time.UnixMilli(1790500000000)

func TestFindDeals_MatchesQueryAndSkipsDeadOffers(t *testing.T) {
	var hits int32
	c := newOffersConnector(newOffersServer(t, &hits), feedNow)

	deals, err := c.FindDeals(context.Background(), "mens winter jacket", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != 2 {
		t.Fatalf("got %d deals, want 2 (live jacket offers only): %+v", len(deals), deals)
	}
	// "Winter Jackets" matches three words (men, winter, jacket), the DOTD
	// puffer matches one — score beats the DOTD tie-break.
	first, second := deals[0], deals[1]
	if first.Title != "Winter Jackets" || first.Kind != deal.KindStoreOffer || first.Source != deal.SourceFlipkartAffiliate {
		t.Fatalf("unexpected first deal %+v", first)
	}
	if first.ImageURL != "https://rukminim1.flixcart.com/mid.jpg" {
		t.Fatalf("ImageURL = %q, want the mid rendition", first.ImageURL)
	}
	if first.StartsAt == nil || first.EndsAt == nil || !first.EndsAt.Equal(time.UnixMilli(1800000000000)) {
		t.Fatalf("window not mapped: %+v", first)
	}
	if second.Title != "Puffer Jackets" || second.Badge != "Deal of the Day" {
		t.Fatalf("unexpected second deal %+v", second)
	}
	if second.ImageURL != "https://rukminim1.flixcart.com/dotd.jpg" {
		t.Fatalf("ImageURL = %q, want the https image (http one refused)", second.ImageURL)
	}
}

func TestFindDeals_PromoWordsDontMatch(t *testing.T) {
	var hits int32
	c := newOffersConnector(newOffersServer(t, &hits), feedNow)
	// "flat" appears in the bedsheet offer ("Flat 60% Off") but is promo
	// filler, not a product word.
	deals, err := c.FindDeals(context.Background(), "running shoes for flat feet", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != 0 {
		t.Fatalf("expected no deals, got %+v", deals)
	}
}

func TestFindDeals_EmptyQueryReturnsHeadlineOffers(t *testing.T) {
	var hits int32
	c := newOffersConnector(newOffersServer(t, &hits), feedNow)
	deals, err := c.FindDeals(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deals) != 3 || deals[0].Badge != "Deal of the Day" {
		t.Fatalf("expected 3 live offers with DOTD first, got %+v", deals)
	}
}

func TestFindDeals_CachesFeed(t *testing.T) {
	var hits int32
	srv := newOffersServer(t, &hits)
	now := feedNow
	c := New(Config{AffiliateID: "aff", AffiliateToken: "tok", OffersBaseURL: srv.URL + "/offers/v1"})
	c.now = func() time.Time { return now }

	for range 3 {
		if _, err := c.FindDeals(context.Background(), "jacket", 5); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected one fetch of each feed, got %d requests", got)
	}
	now = now.Add(offerFeedTTL + time.Second)
	if _, err := c.FindDeals(context.Background(), "jacket", 5); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("expected a refresh after the TTL, got %d requests", got)
	}
}

func TestFindDeals_ServesStaleFeedWhenRefreshFails(t *testing.T) {
	var hits int32
	srv := newOffersServer(t, &hits)
	now := feedNow
	c := New(Config{AffiliateID: "aff", AffiliateToken: "tok", OffersBaseURL: srv.URL + "/offers/v1"})
	c.now = func() time.Time { return now }
	if _, err := c.FindDeals(context.Background(), "jacket", 5); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	now = now.Add(offerFeedTTL + time.Minute)
	deals, err := c.FindDeals(context.Background(), "jacket", 5)
	if err != nil || len(deals) == 0 {
		t.Fatalf("expected the stale feed, got %v, %v", deals, err)
	}
	now = now.Add(offerFeedStale)
	if _, err := c.FindDeals(context.Background(), "jacket", 5); err == nil {
		t.Fatal("expected an error once the cached feed is too old")
	}
}

func TestFindDeals_Unconfigured(t *testing.T) {
	_, err := New(Config{}).FindDeals(context.Background(), "jacket", 5)
	if !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestFindDeals_RejectedCredentials(t *testing.T) {
	var hits int32
	srv := newOffersServer(t, &hits)
	c := New(Config{AffiliateID: "aff", AffiliateToken: "wrong", OffersBaseURL: srv.URL + "/offers/v1"})
	if _, err := c.FindDeals(context.Background(), "jacket", 5); err == nil {
		t.Fatal("expected an error for rejected credentials")
	}
}

func TestDealWordsSingularizes(t *testing.T) {
	got := dealWords("Phone Cases, Glasses & Watches — Flat 50% Off")
	for _, w := range []string{"phone", "case", "glass", "watch"} {
		if !got[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	for _, w := range []string{"flat", "off", "cas"} {
		if got[w] {
			t.Errorf("unexpected %q in %v", w, got)
		}
	}
}
