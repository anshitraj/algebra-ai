package flipkart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// officialResponse follows the Affiliate API reference: productInfoList of
// productBaseInfoV1 objects with {amount, currency} prices.
const officialResponse = `{"productInfoList":[
 {"productBaseInfoV1":{"productId":"MOBGTAGPTB3VS24W","title":"Apple iPhone 15 (Black, 128 GB)","productBrand":"Apple","inStock":true,
  "productUrl":"https://dl.flipkart.com/dl/apple-iphone-15/p/itm6ac6485515ae4?pid=MOBGTAGPTB3VS24W&affid=test","categoryPath":"Mobiles>Apple",
  "maximumRetailPrice":{"amount":79900.0,"currency":"INR"},"flipkartSellingPrice":{"amount":69999.0,"currency":"INR"},"flipkartSpecialPrice":{"amount":65999.0,"currency":"INR"}},
  "productShippingInfoV1":{"shippingCharges":{"amount":0.0,"currency":"INR"},"sellerName":"Seller"}},
 {"productBaseInfoV1":{"productId":"ACCCASE1","title":"Back Case\nfor‮iPhone 15","productBrand":"Generic","inStock":false,
  "productUrl":"https://www.flipkart.com/back-case/p/itm1","flipkartSellingPrice":{"amount":299.5,"currency":"INR"},"flipkartSpecialPrice":{"amount":0,"currency":"INR"}}},
 {"productBaseInfoV1":{"productId":"USD1","title":"Imported","inStock":true,"flipkartSellingPrice":{"amount":10,"currency":"USD"}}}
]}`

func TestUnconfigured_HandoffOnly(t *testing.T) {
	c := New(Config{})
	if c.Capabilities() != (merchant.Capabilities{}) {
		t.Fatalf("expected no capabilities, got %+v", c.Capabilities())
	}
	if st := c.Status(); st.Ready || !strings.Contains(st.Detail, "FLIPKART_AFFILIATE_TOKEN") {
		t.Fatalf("unexpected status %+v", st)
	}
	if _, err := c.SearchProducts(context.Background(), "iphone", 5); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
	link := c.HandoffURL("iphone 15")
	if link != "https://www.flipkart.com/search?q=iphone+15" {
		t.Fatalf("HandoffURL = %q", link)
	}
	if err := merchant.NewAllowedDomains("flipkart.com").ValidateURL(link); err != nil {
		t.Fatal(err)
	}
	res, err := c.ExecuteCheckout(context.Background(), "cart", "approval", merchant.Fulfillment{})
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded {
		t.Fatalf("expected USER_INTERVENTION_REQUIRED, got %+v, %v", res, err)
	}
}

func TestSearchProducts_OfficialResponseShape(t *testing.T) {
	var gotQuery url.Values
	var gotID, gotToken, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		gotID, gotToken = r.Header.Get("Fk-Affiliate-Id"), r.Header.Get("Fk-Affiliate-Token")
		_, _ = w.Write([]byte(officialResponse))
	}))
	defer srv.Close()

	c := New(Config{AffiliateID: "aff-id", AffiliateToken: "aff-token", BaseURL: srv.URL + "/affiliate/1.0"})
	if caps := c.Capabilities(); !caps.Search || caps.Cart || caps.Checkout {
		t.Fatalf("Flipkart is search-only, got %+v", caps)
	}
	products, err := c.SearchProducts(context.Background(), "iphone 15", 5)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if gotPath != "/affiliate/1.0/search.json" || gotQuery.Get("query") != "iphone 15" || gotQuery.Get("resultCount") != "5" ||
		gotID != "aff-id" || gotToken != "aff-token" {
		t.Fatalf("request wrong: path=%s query=%v id=%q token=%q", gotPath, gotQuery, gotID, gotToken)
	}
	if len(products) != 2 {
		t.Fatalf("expected 2 INR products (USD one skipped), got %d: %+v", len(products), products)
	}
	phone, cover := products[0], products[1]
	if phone.MerchantProductID != "MOBGTAGPTB3VS24W" || phone.PriceMinorUnits != 6599900 || phone.Currency != "INR" ||
		phone.Brand != "Apple" || !phone.Available || !strings.HasPrefix(phone.URL, "https://dl.flipkart.com/") || phone.Merchant != Name {
		t.Fatalf("special price / fields mapped wrong: %+v", phone)
	}
	if cover.PriceMinorUnits != 29950 || cover.Available {
		t.Fatalf("selling-price fallback / stock mapped wrong: %+v", cover)
	}
	if strings.ContainsAny(cover.Name, "\n‮") {
		t.Fatalf("merchant text not sanitized: %q", cover.Name)
	}
}

func TestSearchProducts_AcceptsProductsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"products":[{"productBaseInfoV1":{"productId":"P1","title":"Notebook","inStock":true,"flipkartSellingPrice":{"amount":49,"currency":"INR"}}}]}`))
	}))
	defer srv.Close()
	c := New(Config{AffiliateID: "a", AffiliateToken: "b", BaseURL: srv.URL})
	products, err := c.SearchProducts(context.Background(), "notebook", 5)
	if err != nil || len(products) != 1 || products[0].PriceMinorUnits != 4900 {
		t.Fatalf("got %+v, %v", products, err)
	}
}

func TestSearchProducts_HTTPErrors(t *testing.T) {
	for status, want := range map[int]string{401: "rejected", 429: "rate limit", 500: "HTTP 500"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		c := New(Config{AffiliateID: "a", AffiliateToken: "b", BaseURL: srv.URL})
		_, err := c.SearchProducts(context.Background(), "x", 5)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("HTTP %d: expected %q in error, got %v", status, want, err)
		}
	}
}

// The affiliate token is a custom header, which net/http would forward on a
// redirect even to another host.
func TestTokenNeverFollowsRedirect(t *testing.T) {
	var leaked atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Fk-Affiliate-Token") != "" {
			leaked.Add(1)
		}
	}))
	defer attacker.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	c := New(Config{AffiliateID: "a", AffiliateToken: "secret-token", BaseURL: redirector.URL})
	if _, err := c.SearchProducts(context.Background(), "x", 5); err == nil {
		t.Fatal("a redirect must not be treated as success")
	}
	if leaked.Load() != 0 {
		t.Fatal("affiliate token was forwarded to the redirect target")
	}
}

func TestInsecureBaseURLRejected(t *testing.T) {
	c := New(Config{AffiliateID: "a", AffiliateToken: "b", BaseURL: "http://affiliate-api.flipkart.net/affiliate/1.0"})
	if c.Capabilities().Search || !strings.Contains(c.Status().Detail, "https") {
		t.Fatalf("plain-http base URL must disable the connector: %+v", c.Status())
	}
}
