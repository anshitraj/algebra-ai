package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

// serveWith is serve with request headers.
func serveWith(t *testing.T, b *wiring.Bundle, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	NewRouter(b, nil, nil).ServeHTTP(rec, req)
	return rec
}

// A body the caller got wrong is the caller's to fix: 400, never a 500 that
// pages somebody and shows the JSON decoder's internals. With no operator
// token and outside production, B2B registration is open, so these reach the
// decoder without needing a database.
func TestMalformedJSONBodyIs400(t *testing.T) {
	for _, path := range []string{"/api/v1/tenants", "/api/v1/integrators"} {
		for _, body := range []string{`{`, `not json`, ``, `[1,2]`} {
			rec := serveWith(t, &wiring.Bundle{}, "POST", path, body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("POST %s body %q: %d, want 400 (body: %s)", path, body, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "unexpected EOF") || strings.Contains(rec.Body.String(), "invalid character") {
				t.Errorf("POST %s body %q: the JSON decoder's own message leaked: %s", path, body, rec.Body.String())
			}
		}
	}
}

// Every handler maps decodeJSON's error through writeError, so the mapping
// itself is what makes a bad body a 400.
func TestDecodeJSONErrorsMapTo400(t *testing.T) {
	var v struct{ A int }
	for _, body := range []string{`{`, `x`, ``, `{"A":"not a number"}`} {
		err := decodeJSON(httptest.NewRequest("POST", "/", strings.NewReader(body)), &v)
		if err == nil {
			t.Fatalf("body %q decoded without error", body)
		}
		rec := httptest.NewRecorder()
		writeError(rec, err)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: %d, want 400", body, rec.Code)
		}
	}
	// A body past the limit is refused as too large, not read into memory.
	big := strings.NewReader(`{"A":1,"pad":"` + strings.Repeat("x", maxRequestBody+1) + `"}`)
	req := httptest.NewRequest("POST", "/", big)
	rec := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rec, req.Body, maxRequestBody)
	err := decodeJSON(req, &v)
	if err == nil {
		t.Fatal("an oversized body was accepted")
	}
	writeError(rec, err)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: %d, want 413", rec.Code)
	}
}

// The router caps every request body, so no handler has to remember to.
func TestRouterCapsRequestBodies(t *testing.T) {
	huge := `{"name":"` + strings.Repeat("a", maxRequestBody+1) + `"}`
	rec := serveWith(t, &wiring.Bundle{}, "POST", "/api/v1/tenants", huge, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over %d bytes: %d, want 413 (body: %.120s)", maxRequestBody, rec.Code, rec.Body.String())
	}
}

// An intent the caller specified badly is a 400, not the 409 that means the
// request is fine but collides with state.
func TestEconomicIntentValidationErrorsAre400(t *testing.T) {
	now := time.Now()
	passes := memPasses{"pass_mine": {ID: "pass_mine", UserID: "user-1", AgentID: "agent_x", Currency: "USDC",
		BudgetMinorUnits: 10_000_000, BudgetPeriod: spendpass.PeriodTotal, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}}
	b := &wiring.Bundle{
		AuthConfig: config.AuthConfig{DevHeaderAuth: true},
		Economic:   app.NewEconomicService(nil, nil, passes, nil),
	}
	for name, body := range map[string]string{
		"bad capability": `{"spend_pass_id":"pass_mine","capability":"NOT VALID!","budget_max_minor":1000}`,
		"zero budget":    `{"spend_pass_id":"pass_mine","capability":"solana.token-risk","budget_max_minor":0}`,
		"bad window":     `{"spend_pass_id":"pass_mine","capability":"solana.token-risk","budget_max_minor":1000,"window":"has spaces"}`,
		"ttl too long":   `{"spend_pass_id":"pass_mine","capability":"solana.token-risk","budget_max_minor":1000,"ttl_seconds":99999999}`,
	} {
		rec := serveWith(t, b, "POST", "/api/v1/me/economic-intents", body, map[string]string{"X-User-ID": "user-1"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (body: %s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A control character in a post-login destination can turn "/<TAB>/evil.example"
// into the protocol-relative URL "//evil.example" once a browser strips it.
func TestSafeNext_RejectsControlCharacters(t *testing.T) {
	for _, bad := range []string{"/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/\x00/evil.example", "/\x7f", "/ok\t", "/\\/evil.example"} {
		if got := safeNext(bad); got != "" {
			t.Errorf("safeNext(%q) = %q — open redirect", bad, got)
		}
	}
	// Ordinary destinations, including percent-encoded characters (which a
	// browser keeps as path text) and queries, still work.
	for _, ok := range []string{"/console", "/console/passes?tab=active", "/console/orders/ord_1#top", "/console?q=a%20b", "/%09/not-a-host"} {
		if got := safeNext(ok); got != ok {
			t.Errorf("safeNext(%q) = %q, want it kept", ok, got)
		}
	}
}
