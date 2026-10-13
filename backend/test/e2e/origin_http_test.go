package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/project-algebra/algebra/internal/api/v1"
)

// A signed-in browser may change things only from an address the API was told
// is its own site. A site put on a new domain without telling the API hits
// this on every button, so the refusal names the address and the settings.

func postFrom(t *testing.T, url, origin string, cookie *http.Cookie) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out.Error
}

func TestOriginOverHTTP_ChangesAreAcceptedOnlyFromTheSitesOwnAddress(t *testing.T) {
	b, base := serveWith(t, map[string]string{"PUBLIC_WEB_URL": "https://www.algebra.test"})
	who := signUp(t, base, "Ada")
	// The API as cmd/api builds it, with its list from CORS_ALLOWED_ORIGINS: the site's address
	// and the bare name Vercel redirects from.
	api := httptest.NewServer(v1.NewRouter(b, nil, []string{"https://www.algebra.test", "https://algebra.test"}))
	defer api.Close()
	change := api.URL + "/api/v1/me/notifications/email"

	// The site on an address the API doesn't know: refused, saying which and what to set.
	status, msg := postFrom(t, change, "https://www.other-domain.test", who)
	if status != http.StatusForbidden {
		t.Fatalf("an unknown origin must be refused: %d %q", status, msg)
	}
	for _, want := range []string{"cross-origin request rejected", "https://www.other-domain.test", "CORS_ALLOWED_ORIGINS", "PUBLIC_WEB_URL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal says %q: %q", want, msg)
		}
	}

	// Each listed address is accepted (the request then fails on its own merits, not on its origin),
	// and so is a request with no Origin at all: there is no browser to abuse.
	for _, origin := range []string{"https://www.algebra.test", "https://algebra.test", ""} {
		if status, msg := postFrom(t, change, origin, who); status == http.StatusForbidden || strings.Contains(msg, "cross-origin") {
			t.Errorf("origin %q must be accepted: %d %q", origin, status, msg)
		}
	}

	// Without a session cookie there is nothing to protect: sign-in itself is never blocked by this.
	if status, msg := postFrom(t, api.URL+"/api/v1/auth/login", "https://www.other-domain.test", nil); strings.Contains(msg, "cross-origin") {
		t.Errorf("a request with no session is not an origin question: %d %q", status, msg)
	}

	// What a caller sends is echoed bounded and without control characters.
	status, msg = postFrom(t, change, "https://"+strings.Repeat("a", 500)+".test", who)
	if status != http.StatusForbidden || len(msg) > 400 {
		t.Errorf("a long origin is cut: %d, %d characters", status, len(msg))
	}
}
