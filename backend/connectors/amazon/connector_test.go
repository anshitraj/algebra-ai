package amazon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

const searchReply = `{"searchResult":{"items":[
 {"asin":"B0CHX1W1XY","detailPageURL":"https://www.amazon.in/dp/B0CHX1W1XY?tag=tag-21",
  "itemInfo":{"title":{"displayValue":"Anker USB C Cable, 6ft"},"byLineInfo":{"brand":{"displayValue":"Anker"}}},
  "offersV2":{"listings":[{"price":{"money":{"amount":1299.0,"currency":"INR","displayAmount":"₹1,299.00"}},"availability":{"type":"IN_STOCK","maxOrderQuantity":10}}]}},
 {"asin":"B000NOOFFER","detailPageURL":"https://www.amazon.in/dp/B000NOOFFER","itemInfo":{"title":{"displayValue":"No offer item"}}},
 {"asin":"B0OUTSTOCK1","detailPageURL":"https://www.amazon.in/dp/B0OUTSTOCK1",
  "itemInfo":{"title":{"displayValue":"Braided Cable"}},
  "offersV2":{"listings":[{"price":{"money":{"amount":499.5,"currency":"INR"}},"availability":{"type":"OUT_OF_STOCK"}}]}}
],"totalResultCount":3}}`

type fakeAmazon struct {
	tokenSrv, apiSrv *httptest.Server
	tokenCalls       atomic.Int32

	mu              sync.Mutex
	status          int
	reply           string
	lastAuth        string
	lastMarketplace string
	lastBody        map[string]any
}

func newFakeAmazon(t *testing.T, wantScope string) *fakeAmazon {
	t.Helper()
	f := &fakeAmazon{status: http.StatusOK, reply: searchReply}
	f.tokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		id, secret, basic := r.BasicAuth()
		if !basic {
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if id != "cred-id" || secret != "cred-secret" || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != wantScope {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		f.tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"amzn-token","token_type":"bearer","expires_in":3600}`))
	}))
	f.apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (r.URL.Path != "/catalog/v1/searchItems" && r.URL.Path != "/catalog/v1/getItems") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.lastAuth, f.lastMarketplace, f.lastBody = r.Header.Get("Authorization"), r.Header.Get("x-marketplace"), body
		status, reply := f.status, f.reply
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(f.tokenSrv.Close)
	t.Cleanup(f.apiSrv.Close)
	return f
}

func (f *fakeAmazon) connector(version string) *Connector {
	return New(Config{
		CredentialID: "cred-id", CredentialSecret: "cred-secret", CredentialVersion: version, PartnerTag: "tag-21",
		APIBase: f.apiSrv.URL + "/catalog/v1", TokenURL: f.tokenSrv.URL,
	})
}

func TestUnconfigured_HandoffOnly(t *testing.T) {
	c := New(Config{})
	if c.Capabilities() != (merchant.Capabilities{}) || !strings.Contains(c.Status().Detail, "AMAZON_ASSOCIATE_TAG") {
		t.Fatalf("unexpected %+v / %+v", c.Capabilities(), c.Status())
	}
	if _, err := c.SearchProducts(context.Background(), "cable", 5); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
	link := c.HandoffURL("usb c cable")
	if link != "https://www.amazon.in/s?k=usb+c+cable" {
		t.Fatalf("HandoffURL = %q", link)
	}
	if err := merchant.NewAllowedDomains("amazon.in").ValidateURL(link); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownCredentialVersion(t *testing.T) {
	c := New(Config{CredentialID: "id", CredentialSecret: "s", PartnerTag: "tag-21", CredentialVersion: "9.9"})
	if c.Capabilities().Search || !strings.Contains(c.Status().Detail, "9.9") {
		t.Fatalf("an unknown credential version must disable search: %+v", c.Status())
	}
}

func TestSearchItems_V3Credential(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	c := f.connector("3.2")
	ctx := context.Background()

	products, err := c.SearchProducts(ctx, "usb c cable", 5)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	f.mu.Lock()
	auth, marketplace, body := f.lastAuth, f.lastMarketplace, f.lastBody
	f.mu.Unlock()
	if auth != "Bearer amzn-token" || marketplace != "www.amazon.in" {
		t.Fatalf("headers wrong: Authorization=%q x-marketplace=%q", auth, marketplace)
	}
	resources, _ := body["resources"].([]any)
	if body["keywords"] != "usb c cable" || body["partnerTag"] != "tag-21" || body["marketplace"] != "www.amazon.in" ||
		body["itemCount"] != float64(5) || !slices.Contains(resources, any("offersV2.listings.price")) {
		t.Fatalf("request body wrong: %v", body)
	}

	if len(products) != 2 {
		t.Fatalf("expected 2 priced items (no-offer item skipped), got %+v", products)
	}
	cable, braided := products[0], products[1]
	if cable.MerchantProductID != "B0CHX1W1XY" || cable.PriceMinorUnits != 129900 || cable.Currency != "INR" || cable.Brand != "Anker" ||
		!cable.Available || cable.URL != "https://www.amazon.in/dp/B0CHX1W1XY?tag=tag-21" || cable.Merchant != Name {
		t.Fatalf("item mapped wrong: %+v", cable)
	}
	if braided.PriceMinorUnits != 49950 || braided.Available {
		t.Fatalf("out-of-stock item mapped wrong: %+v", braided)
	}

	if _, err := c.SearchProducts(ctx, "hdmi", 3); err != nil {
		t.Fatal(err)
	}
	if f.tokenCalls.Load() != 1 {
		t.Fatalf("the access token should be cached, got %d token requests", f.tokenCalls.Load())
	}
}

func TestSearchItems_V2CredentialAddsVersion(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi/default")
	if _, err := f.connector("2.2").SearchProducts(context.Background(), "cable", 5); err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if f.lastAuth != "Bearer amzn-token, Version 2.2" {
		t.Fatalf("Authorization = %q", f.lastAuth)
	}
}

func TestSearchItems_NoResultsIsEmptyNotError(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	f.status, f.reply = http.StatusNotFound, `{"errors":[{"code":"NoResults","message":"No results found for your request."}]}`
	products, err := f.connector("3.2").SearchProducts(context.Background(), "zzzz", 5)
	if err != nil || len(products) != 0 {
		t.Fatalf("got %+v, %v", products, err)
	}
}

func TestSearchItems_Errors(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	c := f.connector("3.2")

	f.status, f.reply = http.StatusUnauthorized, `{"errors":[{"code":"InvalidToken","message":"token expired"}]}`
	if _, err := c.SearchProducts(context.Background(), "x", 5); err == nil || !strings.Contains(err.Error(), "rejected") || !strings.Contains(err.Error(), "InvalidToken") {
		t.Fatalf("401: got %v", err)
	}
	f.status, f.reply = http.StatusTooManyRequests, `{}`
	if _, err := c.SearchProducts(context.Background(), "x", 5); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("429: got %v", err)
	}
}

const getItemsReply = `{"itemsResult":{"items":[
 {"asin":"B0CHX1W1XY","detailPageURL":"https://www.amazon.in/dp/B0CHX1W1XY?tag=tag-21",
  "itemInfo":{"title":{"displayValue":"Anker USB C Cable, 6ft"},"byLineInfo":{"brand":{"displayValue":"Anker"}}},
  "offersV2":{"listings":[{"price":{"money":{"amount":1299.0,"currency":"INR"}},"availability":{"type":"IN_STOCK"}}]}}
]}}`

func TestGetProduct_ByASIN(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	f.reply = getItemsReply
	c := f.connector("3.2")

	p, err := c.GetProduct(context.Background(), "b0chx1w1xy") // lowercase input, normalized
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if p.MerchantProductID != "B0CHX1W1XY" || p.PriceMinorUnits != 129900 || p.Currency != "INR" || p.Brand != "Anker" || !p.Available || p.Confidence != 1.0 {
		t.Fatalf("product mapped wrong: %+v", p)
	}
	f.mu.Lock()
	body := f.lastBody
	f.mu.Unlock()
	itemIDs, _ := body["itemIds"].([]any)
	if body["itemIdType"] != "ASIN" || len(itemIDs) != 1 || itemIDs[0] != "B0CHX1W1XY" || body["partnerTag"] != "tag-21" {
		t.Fatalf("getItems request body wrong: %v", body)
	}
}

func TestGetProduct_InvalidASIN(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	if _, err := f.connector("3.2").GetProduct(context.Background(), "not-an-asin"); err == nil || !strings.Contains(err.Error(), "not a valid ASIN") {
		t.Fatalf("expected invalid-ASIN error, got %v", err)
	}
}

func TestGetProduct_NotFound(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	f.reply = `{"itemsResult":{"items":[]},"errors":[{"code":"InvalidParameterValue","message":"unknown ASIN"}]}`
	_, err := f.connector("3.2").GetProduct(context.Background(), "B0000000XX")
	if !errors.Is(err, shared.ErrNotFound) || !strings.Contains(err.Error(), "InvalidParameterValue") {
		t.Fatalf("expected wrapped ErrNotFound, got %v", err)
	}
}

func TestGetProduct_Unconfigured(t *testing.T) {
	c := New(Config{})
	if _, err := c.GetProduct(context.Background(), "B0CHX1W1XY"); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestCartURL(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	c := f.connector("3.2")

	link, err := c.CartURL([]CartLine{{ASIN: "b0chx1w1xy", Quantity: 2}, {ASIN: "B0TESTITEM", Quantity: 1}})
	if err != nil {
		t.Fatalf("CartURL: %v", err)
	}
	want := "https://www.amazon.in/gp/aws/cart/add.html?ASIN.1=B0CHX1W1XY&ASIN.2=B0TESTITEM&AssociateTag=tag-21&Quantity.1=2&Quantity.2=1"
	if link != want {
		t.Fatalf("CartURL = %q, want %q", link, want)
	}
	if err := merchant.NewAllowedDomains("amazon.in").ValidateURL(link); err != nil {
		t.Fatal(err)
	}
}

func TestCartURL_Validation(t *testing.T) {
	f := newFakeAmazon(t, "creatorsapi::default")
	c := f.connector("3.2")

	if _, err := c.CartURL(nil); err == nil {
		t.Fatal("expected error for empty items")
	}
	if _, err := c.CartURL([]CartLine{{ASIN: "bad", Quantity: 1}}); err == nil || !strings.Contains(err.Error(), "not a valid ASIN") {
		t.Fatalf("expected ASIN validation error, got %v", err)
	}
	if _, err := c.CartURL([]CartLine{{ASIN: "B0CHX1W1XY", Quantity: 0}}); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("expected quantity validation error, got %v", err)
	}
	if _, err := New(Config{}).CartURL([]CartLine{{ASIN: "B0CHX1W1XY", Quantity: 1}}); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented for unconfigured connector, got %v", err)
	}
}

func TestTokenFailureDoesNotLeakSecret(t *testing.T) {
	f := newFakeAmazon(t, "some-other-scope") // every token request is rejected
	_, err := f.connector("3.2").SearchProducts(context.Background(), "x", 5)
	if err == nil || !strings.Contains(err.Error(), "could not obtain") {
		t.Fatalf("expected a token error, got %v", err)
	}
	if strings.Contains(err.Error(), "cred-secret") {
		t.Fatal("error message contains the credential secret")
	}
}
