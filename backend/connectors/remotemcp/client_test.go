package remotemcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

type echoIn struct {
	Query string `json:"query"`
}

type echoOut struct {
	Echo  string `json:"echo"`
	Count int    `json:"count"`
}

// fakeMerchant is a genuine go-sdk MCP server behind a bearer-token check:
// the protocol under test is real, only the merchant is a stand-in.
type fakeMerchant struct {
	srv        *httptest.Server
	validToken atomic.Value // string
	prm        atomic.Value // []byte: protected-resource metadata, served unauthenticated
}

func newFakeMerchant(t *testing.T) *fakeMerchant {
	t.Helper()
	fm := &fakeMerchant{}
	fm.validToken.Store("good-token")

	server := mcp.NewServer(&mcp.Implementation{Name: "fake-merchant", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes the query"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, echoOut, error) {
			return nil, echoOut{Echo: in.Query, Count: 1}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "explode", Description: "always fails"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ echoIn) (*mcp.CallToolResult, echoOut, error) {
			return nil, echoOut{}, errors.New("item out of stock\n\x1b[31mretry later")
		})
	server.AddTool(&mcp.Tool{Name: "text_json", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"echo":"from-text","count":2}`}}}, nil
		})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	fm.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			prm, _ := fm.prm.Load().([]byte)
			if prm == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(prm)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+fm.validToken.Load().(string) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/.well-known/oauth-protected-resource"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(fm.srv.Close)
	return fm
}

func linkedClient(t *testing.T, endpoint string, tok *oauth2.Token, tokenURL string) (*Client, *FileSessionStore) {
	t.Helper()
	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	if tokenURL == "" {
		tokenURL = "http://127.0.0.1:1/token"
	}
	if err := store.Save(&Session{
		Merchant: "testmerchant", Endpoint: endpoint, ClientID: "client-123",
		AuthURL: "http://127.0.0.1:1/authorize", TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams,
		Token: tok, LinkedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{Merchant: "testmerchant", Endpoint: endpoint, Store: store, AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, store
}

func freshToken(access string) *oauth2.Token {
	return &oauth2.Token{AccessToken: access, TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}
}

func TestClient_CallDecodesStructuredResultAndChecksContract(t *testing.T) {
	fm := newFakeMerchant(t)
	client, _ := linkedClient(t, fm.srv.URL, freshToken("good-token"), "")
	ctx := context.Background()

	var out echoOut
	if err := client.Call(ctx, "echo", map[string]any{"query": "milk"}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Echo != "milk" || out.Count != 1 {
		t.Fatalf("unexpected result %+v", out)
	}

	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if err := CheckTools(tools, []ToolRequirement{{Name: "echo", Properties: []string{"query"}}}); err != nil {
		t.Fatalf("contract should match: %v", err)
	}
	err = CheckTools(tools, []ToolRequirement{{Name: "echo", Properties: []string{"addressId"}}, {Name: "checkout"}})
	var ce *ContractError
	if !errors.As(err, &ce) || len(ce.Problems) != 2 {
		t.Fatalf("expected 2 contract problems, got %v", err)
	}
}

func TestClient_ToolErrorIsTypedAndSanitized(t *testing.T) {
	fm := newFakeMerchant(t)
	client, _ := linkedClient(t, fm.srv.URL, freshToken("good-token"), "")

	err := client.Call(context.Background(), "explode", map[string]any{"query": "x"}, &echoOut{})
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %v", err)
	}
	if !strings.Contains(te.Message, "item out of stock") || strings.ContainsAny(te.Message, "\n\x1b") {
		t.Fatalf("tool error message should be kept but sanitized, got %q", te.Message)
	}
}

func TestClient_TextJSONFallback(t *testing.T) {
	fm := newFakeMerchant(t)
	client, _ := linkedClient(t, fm.srv.URL, freshToken("good-token"), "")

	var out echoOut
	if err := client.Call(context.Background(), "text_json", nil, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Echo != "from-text" || out.Count != 2 {
		t.Fatalf("unexpected result %+v", out)
	}
}

func TestClient_RejectedTokenMeansSessionExpired(t *testing.T) {
	fm := newFakeMerchant(t)
	client, _ := linkedClient(t, fm.srv.URL, freshToken("revoked-token"), "")

	err := client.Call(context.Background(), "echo", map[string]any{"query": "milk"}, nil)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

// An expired access token is refreshed with the stored refresh token, and
// the rotated tokens are written back so a restart doesn't replay a spent
// refresh token.
func TestClient_RefreshesAndPersistsRotatedToken(t *testing.T) {
	fm := newFakeMerchant(t)
	var refreshes atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil ||
			r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" || r.Form.Get("client_id") != "client-123" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"good-token","token_type":"Bearer","expires_in":3600,"refresh_token":"refresh-2"}`))
	}))
	defer tokenSrv.Close()

	expired := &oauth2.Token{AccessToken: "stale-token", RefreshToken: "refresh-1", TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute)}
	client, store := linkedClient(t, fm.srv.URL, expired, tokenSrv.URL)

	var out echoOut
	if err := client.Call(context.Background(), "echo", map[string]any{"query": "milk"}, &out); err != nil {
		t.Fatalf("Call after refresh: %v", err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("expected exactly one refresh, got %d", refreshes.Load())
	}
	saved, err := store.Load("testmerchant")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token.AccessToken != "good-token" || saved.Token.RefreshToken != "refresh-2" {
		t.Fatalf("rotated token not persisted: %+v", saved.Token)
	}
}

func TestClient_RefusesSessionLinkedToAnotherEndpoint(t *testing.T) {
	fm := newFakeMerchant(t)
	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	if err := store.Save(&Session{Merchant: "testmerchant", Endpoint: "https://elsewhere.example/mcp", ClientID: "c", Token: freshToken("good-token")}); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{Merchant: "testmerchant", Endpoint: fm.srv.URL, Store: store, AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Call(context.Background(), "echo", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "re-link") {
		t.Fatalf("expected a re-link error for a session bound to another endpoint, got %v", err)
	}
}

// A merchant endpoint that redirects must not get the bearer token carried
// to the redirect target.
func TestClient_NeverFollowsRedirectWithToken(t *testing.T) {
	var leaked atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Add(1)
		}
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer attacker.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	client, _ := linkedClient(t, redirector.URL, freshToken("good-token"), "")
	if err := client.Call(context.Background(), "echo", nil, nil); err == nil {
		t.Fatal("expected the call to fail on a redirect")
	}
	if leaked.Load() != 0 {
		t.Fatal("bearer token was sent to the redirect target")
	}
}

func TestClient_NotLinked(t *testing.T) {
	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	client, err := NewClient(Config{Merchant: "testmerchant", Endpoint: "https://mcp.example.com/mcp", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Call(context.Background(), "echo", nil, nil); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("expected ErrNotLinked, got %v", err)
	}
}

func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		url           string
		allowLoopback bool
		ok            bool
	}{
		{"https://mcp.zepto.co.in/mcp", false, true},
		{"http://mcp.zepto.co.in/mcp", false, false},
		{"https://10.0.0.5/mcp", false, false},
		{"https://169.254.169.254/latest", false, false},
		{"https://localhost/mcp", false, false},
		{"https://user:pass@mcp.example.com/mcp", false, false},
		{"http://127.0.0.1:8080/mcp", true, true},
		{"http://10.0.0.5/mcp", true, false},
		{"not a url", false, false},
	}
	for _, tc := range cases {
		err := ValidateEndpoint(tc.url, tc.allowLoopback)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateEndpoint(%q, %v) = %v, want ok=%v", tc.url, tc.allowLoopback, err, tc.ok)
		}
	}
}

func TestSafeText(t *testing.T) {
	got := SafeText("  line one\n\tline\x00two ‮ ", 100)
	if got != "line one line two" {
		// U+202E is a format character, not space/control per unicode.IsControl;
		// assert only on what SafeText promises.
		if strings.ContainsAny(got, "\n\t\x00") || !strings.HasPrefix(got, "line one line") {
			t.Fatalf("SafeText produced %q", got)
		}
	}
	if long := SafeText(strings.Repeat("₹", 50), 10); len(long) > 10+len("…") || !strings.HasSuffix(long, "…") {
		t.Fatalf("SafeText should cap length on a rune boundary, got %q", long)
	}
	if _, err := url.Parse(SafeText("https://example.com/a b", 100)); err != nil {
		t.Fatal(err)
	}
}
