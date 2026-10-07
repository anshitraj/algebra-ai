package monid

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/providers/catalog"
)

func TestSnapshotListsMonidToolsAsNotPayable(t *testing.T) {
	c := New()
	l, err := c.List(context.Background(), catalog.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Total < 20 {
		t.Fatalf("only %d Monid providers", l.Total)
	}
	d, err := c.Detail(context.Background(), "exa")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "monid:exa" || d.Billing != "monid-balance" || len(d.Endpoints) == 0 {
		t.Fatalf("exa: %+v", d.Provider)
	}
	var search *catalog.Endpoint
	for i := range d.Endpoints {
		if d.Endpoints[i].Path == "/search" {
			search = &d.Endpoints[i]
		}
		if d.Endpoints[i].Callable || d.Endpoints[i].Reason != NotPayable {
			t.Fatalf("a Monid endpoint must not be callable through Algebra yet: %+v", d.Endpoints[i])
		}
	}
	if search == nil || search.PriceMinor != 7_000 || !strings.Contains(search.Pricing, "$0.007") {
		t.Fatalf("exa search price: %+v", search)
	}
	// Routing never gets a Monid candidate.
	if cs, err := c.CandidatesFor(context.Background(), "monid:exa", search.Capability); err != nil || len(cs) != 0 {
		t.Fatalf("candidates: %v %v", cs, err)
	}
}

func TestMonidStaysOutOfSolanaListingsUnlessAskedFor(t *testing.T) {
	c := New()
	l, _ := c.List(context.Background(), catalog.Filter{Network: "solana"})
	if l.Total != 0 {
		t.Fatalf("a mainnet listing must not show off-chain providers, got %d", l.Total)
	}
	l, _ = c.List(context.Background(), catalog.Filter{Network: "solana", Source: Source})
	if l.Total == 0 {
		t.Fatal("asking for Monid by name lists it on any network")
	}
	l, _ = c.List(context.Background(), catalog.Filter{Query: "people data"})
	if l.Total == 0 {
		t.Fatal("search by words should find People Data Labs")
	}
}

func TestMonidEndpointsJoinClassesForComparison(t *testing.T) {
	c := New()
	d, _ := c.Detail(context.Background(), "monid:exa")
	for _, e := range d.Endpoints {
		if e.Path == "/search" {
			if id, ok := routing.Classify(e.Path, e.Description); !ok || id != "web.search" {
				t.Fatalf("exa /search classified as %q", id)
			}
		}
	}
}

// The client against Monid's documented request and response shapes.
func TestClientDiscoverAndRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer monid_test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/discover":
			var req struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(b, &req)
			if req.Query != "twitter posts about AI" || req.Limit != 5 {
				t.Errorf("discover request: %s", b)
			}
			_, _ = w.Write([]byte(`{"results":[{"provider":"apify","providerName":"Apify","endpoint":"/apidojo/tweet-scraper","description":"Scrape tweets","price":{"type":"PER_CALL","amount":0.003,"currency":"USD"}}],"query":"twitter posts about AI","count":1}`))
		case "/v1/run":
			_, _ = w.Write([]byte(`{"runId":"01HXYZ","provider":"pdl","endpoint":"/person/enrich","status":"COMPLETED","output":{"full_name":"John Doe"},"providerResponse":{"httpStatus":200},"price":{"type":"PER_CALL","amount":0.003,"currency":"USD"},"billing":{"calculatedCost":{"value":3000,"unit":"MICRO_DOLLAR","currency":"USD"},"actualCost":{"value":0,"unit":"MICRO_DOLLAR","currency":"USD"},"reportedCost":{"value":3000,"unit":"MICRO_DOLLAR","currency":"USD"}},"resultCount":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient("monid_test")
	c.BaseURL = srv.URL + "/v1"
	rs, err := c.Discover(context.Background(), "twitter posts about AI", 5)
	if err != nil || len(rs) != 1 || rs[0].Endpoint != "/apidojo/tweet-scraper" || rs[0].Price.Minor() != 3_000 {
		t.Fatalf("discover: %v %+v", err, rs)
	}
	run, err := c.StartRun(context.Background(), "pdl", "/person/enrich", json.RawMessage(`{"email":"a@b.c"}`))
	if err != nil || !run.Done() || !run.Delivered() || run.Charged() != 3_000 {
		t.Fatalf("run: %v %+v", err, run)
	}
	bad := NewClient("wrong")
	bad.BaseURL = srv.URL + "/v1"
	if _, err := bad.Discover(context.Background(), "x", 1); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a bad key must fail plainly: %v", err)
	}
	if _, err := NewClient("").Discover(context.Background(), "x", 1); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
}
