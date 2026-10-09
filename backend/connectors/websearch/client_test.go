package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const officialReply = `{"items":[
 {"title":"Buy Wireless Mouse Online","link":"https://example-store.com/p/mouse","snippet":"Ergonomic wireless mouse, ships in 2 days."},
 {"title":"No link item","snippet":"missing link, must be dropped"},
 {"title":"Internal probe","link":"https://169.254.169.254/latest/meta-data/","snippet":"must be dropped: SSRF-shaped"},
 {"title":"Insecure","link":"http://example-store.com/p/plain","snippet":"must be dropped: not https"}
]}`

func fakeServer(t *testing.T, status int, reply string) (*httptest.Server, *string) {
	t.Helper()
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &lastQuery
}

func TestUnconfigured(t *testing.T) {
	c := New(Config{})
	if c.Configured() {
		t.Fatal("expected unconfigured")
	}
	if !strings.Contains(c.ConfigError(), "GOOGLE_SEARCH_API_KEY") {
		t.Fatalf("unexpected config error: %s", c.ConfigError())
	}
	if _, err := c.Search(context.Background(), "mouse", 5); err == nil || !strings.Contains(err.Error(), "GOOGLE_SEARCH_API_KEY") {
		t.Fatalf("expected config error from Search, got %v", err)
	}
}

func TestSearch_DropsUnsafeAndIncompleteResults(t *testing.T) {
	srv, lastQuery := fakeServer(t, http.StatusOK, officialReply)
	c := New(Config{APIKey: "key123", SearchEngineID: "cx456", APIBase: srv.URL})

	results, err := c.Search(context.Background(), "wireless mouse", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected only the one safe, complete result, got %+v", results)
	}
	r := results[0]
	if r.Title != "Buy Wireless Mouse Online" || r.URL != "https://example-store.com/p/mouse" || !strings.Contains(r.Snippet, "Ergonomic") {
		t.Fatalf("result mapped wrong: %+v", r)
	}
	if !strings.Contains(*lastQuery, "key=key123") || !strings.Contains(*lastQuery, "cx=cx456") || !strings.Contains(*lastQuery, "q=wireless") {
		t.Fatalf("request query wrong: %s", *lastQuery)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	c := New(Config{APIKey: "k", SearchEngineID: "cx"})
	if _, err := c.Search(context.Background(), "   ", 5); err == nil || !strings.Contains(err.Error(), "empty search query") {
		t.Fatalf("expected empty-query error, got %v", err)
	}
}

func TestSearch_APIErrors(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusForbidden, `{"error":{"code":403,"message":"API key not valid"}}`)
	c := New(Config{APIKey: "bad", SearchEngineID: "cx", APIBase: srv.URL})
	_, err := c.Search(context.Background(), "x", 5)
	if err == nil || !strings.Contains(err.Error(), "rejected") || !strings.Contains(err.Error(), "API key not valid") {
		t.Fatalf("expected rejected-credentials error, got %v", err)
	}
}

func TestSearch_RateLimit(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusTooManyRequests, `{}`)
	c := New(Config{APIKey: "k", SearchEngineID: "cx", APIBase: srv.URL})
	if _, err := c.Search(context.Background(), "x", 5); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
}

func TestSearch_NoResultsIsEmptyNotError(t *testing.T) {
	srv, _ := fakeServer(t, http.StatusOK, `{}`)
	c := New(Config{APIKey: "k", SearchEngineID: "cx", APIBase: srv.URL})
	results, err := c.Search(context.Background(), "zzzz-nothing", 5)
	if err != nil || len(results) != 0 {
		t.Fatalf("got %+v, %v", results, err)
	}
}
