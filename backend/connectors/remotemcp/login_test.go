package remotemcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuthServer is a minimal OAuth 2.1 authorization server that enforces
// what a real one must: registered redirect URI, PKCE S256 verifier, the
// resource indicator, and a single authorization code.
type fakeAuthServer struct {
	srv *httptest.Server

	mu            sync.Mutex
	pkceMethods   []string
	stateOverride string
	deny          bool
	registration  map[string]any
	authorizeQ    url.Values
	challenge     string
	tokenCalls    int
}

func writeTestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newFakeAuthServer(t *testing.T, resource string) *fakeAuthServer {
	t.Helper()
	a := &fakeAuthServer{pkceMethods: []string{"S256"}}
	mux := http.NewServeMux()
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)

	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		methods := a.pkceMethods
		a.mu.Unlock()
		writeTestJSON(w, http.StatusOK, map[string]any{
			"issuer":                                a.srv.URL,
			"authorization_endpoint":                a.srv.URL + "/authorize",
			"token_endpoint":                        a.srv.URL + "/token",
			"registration_endpoint":                 a.srv.URL + "/register",
			"response_types_supported":              []string{"code"},
			"code_challenge_methods_supported":      methods,
			"token_endpoint_auth_methods_supported": []string{"none"},
			"scopes_supported":                      []string{"tools:read", "tools:write", "offline_access"},
		})
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		a.registration = body
		a.mu.Unlock()
		writeTestJSON(w, http.StatusCreated, map[string]any{"client_id": "dyn-client", "token_endpoint_auth_method": "none", "redirect_uris": body["redirect_uris"]})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a.mu.Lock()
		a.authorizeQ = q
		a.challenge = q.Get("code_challenge")
		state, deny := q.Get("state"), a.deny
		if a.stateOverride != "" {
			state = a.stateOverride
		}
		a.mu.Unlock()
		target := q.Get("redirect_uri") + "?state=" + url.QueryEscape(state)
		if deny {
			target += "&error=access_denied"
		} else {
			target += "&code=auth-code-1"
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.tokenCalls++
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		redirects, _ := a.registration["redirect_uris"].([]any)
		switch {
		case r.Form.Get("grant_type") != "authorization_code",
			r.Form.Get("code") != "auth-code-1",
			r.Form.Get("client_id") != "dyn-client",
			r.Form.Get("resource") != resource,
			len(redirects) != 1 || r.Form.Get("redirect_uri") != redirects[0],
			base64.RawURLEncoding.EncodeToString(sum[:]) != a.challenge:
			writeTestJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		writeTestJSON(w, http.StatusOK, map[string]any{"access_token": "good-token", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "refresh-1"})
	})
	return a
}

func (fm *fakeMerchant) setPRM(t *testing.T, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	fm.prm.Store(raw)
}

func linkFixture(t *testing.T) (*fakeMerchant, *fakeAuthServer) {
	t.Helper()
	fm := newFakeMerchant(t)
	as := newFakeAuthServer(t, fm.srv.URL)
	fm.setPRM(t, map[string]any{
		"resource":              fm.srv.URL,
		"authorization_servers": []string{as.srv.URL},
		"scopes_supported":      []string{"tools:read", "tools:write", "dev.ucp.shopping.cart:manage"},
	})
	return fm, as
}

func freeRedirectURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/callback"
}

// testBrowser plays the user's browser: it opens the authorization URL and
// follows the authorization server's redirect back to the loopback callback.
type testBrowser struct {
	mu       sync.Mutex
	lastPage string
	err      error
}

func (b *testBrowser) open(authURL string) {
	resp, err := http.Get(authURL)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.err = err
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	b.lastPage = string(body)
}

func loginOpts(t *testing.T, b *testBrowser) LoginOptions {
	return LoginOptions{RedirectURL: freeRedirectURL(t), ShowURL: b.open, AllowInsecureLoopback: true, Timeout: 30 * time.Second}
}

func TestLogin_EndToEndThenCall(t *testing.T) {
	fm, as := linkFixture(t)
	b := &testBrowser{}
	ctx := context.Background()

	sess, err := Login(ctx, "testmerchant", fm.srv.URL, loginOpts(t, b))
	if err != nil {
		t.Fatalf("Login: %v (browser error: %v)", err, b.err)
	}
	if sess.ClientID != "dyn-client" || sess.Token.AccessToken != "good-token" || sess.Token.RefreshToken != "refresh-1" ||
		sess.Resource != fm.srv.URL || sess.Issuer != as.srv.URL || sess.Endpoint != fm.srv.URL || sess.ClientSecret != "" {
		t.Fatalf("unexpected session %+v", sess)
	}
	if !strings.Contains(b.lastPage, "Account linked") {
		t.Fatalf("callback page = %q", b.lastPage)
	}

	as.mu.Lock()
	reg, q := as.registration, as.authorizeQ
	as.mu.Unlock()
	if reg["token_endpoint_auth_method"] != "none" {
		t.Fatalf("expected a public-client registration, got %v", reg["token_endpoint_auth_method"])
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("resource") != fm.srv.URL || q.Get("state") == "" {
		t.Fatalf("authorization request missing PKCE/resource/state: %v", q)
	}
	// Every advertised scope is requested up front — a server process can't
	// do interactive step-up later.
	scopes := strings.Fields(q.Get("scope"))
	for _, want := range []string{"tools:read", "tools:write", "dev.ucp.shopping.cart:manage", "offline_access"} {
		if !slices.Contains(scopes, want) {
			t.Fatalf("scope %q not requested (got %v)", want, scopes)
		}
	}

	store := NewFileSessionStore(t.TempDir(), testEncryptor(t))
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{Merchant: "testmerchant", Endpoint: fm.srv.URL, Store: store, AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var out echoOut
	if err := client.Call(ctx, "echo", map[string]any{"query": "linked"}, &out); err != nil || out.Echo != "linked" {
		t.Fatalf("calling with the linked session: %+v, %v", out, err)
	}
}

func TestLogin_StateMismatchAbortsBeforeTokenExchange(t *testing.T) {
	fm, as := linkFixture(t)
	as.stateOverride = "forged-state"
	_, err := Login(context.Background(), "testmerchant", fm.srv.URL, loginOpts(t, &testBrowser{}))
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected state mismatch, got %v", err)
	}
	if as.tokenCalls != 0 {
		t.Fatal("a forged state must never reach the token endpoint")
	}
}

func TestLogin_UserDenied(t *testing.T) {
	fm, as := linkFixture(t)
	as.deny = true
	_, err := Login(context.Background(), "testmerchant", fm.srv.URL, loginOpts(t, &testBrowser{}))
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("expected access_denied, got %v", err)
	}
	if as.tokenCalls != 0 {
		t.Fatal("a denied authorization must not exchange a code")
	}
}

func TestLogin_RequiresPKCES256(t *testing.T) {
	fm, as := linkFixture(t)
	as.pkceMethods = []string{"plain"}
	_, err := Login(context.Background(), "testmerchant", fm.srv.URL, loginOpts(t, &testBrowser{}))
	if err == nil || !strings.Contains(err.Error(), "S256") {
		t.Fatalf("expected refusal without S256, got %v", err)
	}
}

func TestLogin_RefusesMismatchedResource(t *testing.T) {
	fm, as := linkFixture(t)
	fm.setPRM(t, map[string]any{"resource": "https://evil.example/mcp", "authorization_servers": []string{as.srv.URL}})
	_, err := Login(context.Background(), "testmerchant", fm.srv.URL, loginOpts(t, &testBrowser{}))
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected refusal for a mismatched resource, got %v", err)
	}
}

func TestParseLoopbackRedirect(t *testing.T) {
	good := map[string][2]string{
		"http://127.0.0.1:8765/callback": {"127.0.0.1:8765", "/callback"},
		"http://localhost:9000":          {"127.0.0.1:9000", "/"},
		"http://[::1]:7000/cb":           {"[::1]:7000", "/cb"},
	}
	for raw, want := range good {
		addr, path, err := parseLoopbackRedirect(raw)
		if err != nil || addr != want[0] || path != want[1] {
			t.Errorf("parseLoopbackRedirect(%q) = %q, %q, %v", raw, addr, path, err)
		}
	}
	for _, raw := range []string{
		"https://127.0.0.1:8765/callback", // loopback redirects are plain http by definition
		"http://example.com:8765/callback",
		"http://127.0.0.1/callback", // no port
		"http://0.0.0.0:8765/callback",
		"http://127.0.0.1:8765/callback?x=1",
	} {
		if _, _, err := parseLoopbackRedirect(raw); err == nil {
			t.Errorf("parseLoopbackRedirect(%q) should fail", raw)
		}
	}
}

func TestCallbackHandler_StaticPageAndSingleUse(t *testing.T) {
	results := make(chan callbackResult, 1)
	h := callbackHandler("/callback", results)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?error=%3Cscript%3Ealert(1)%3C%2Fscript%3E&state=s", nil))
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("callback page reflected query input")
	}
	if res := <-results; res.err != "authorization error" {
		t.Fatalf("a malformed error code should be replaced, got %q", res.err)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?code=again&state=s", nil))
	if !strings.Contains(rec.Body.String(), "already used") {
		t.Fatalf("second redirect should be rejected, got %q", rec.Body.String())
	}
	select {
	case <-results:
		t.Fatal("only the first redirect may be delivered")
	default:
	}
}
