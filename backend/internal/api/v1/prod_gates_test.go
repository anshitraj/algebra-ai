package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

func apiWith(auth config.AuthConfig) *API {
	return &API{b: &wiring.Bundle{AuthConfig: auth}}
}

func TestRequireOperator(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name   string
		auth   config.AuthConfig
		header string
		ok     bool
	}{
		{"development without a token stays open", config.AuthConfig{}, "", true},
		{"production without a token is closed", config.AuthConfig{Production: true}, "", false},
		{"configured token must be sent", config.AuthConfig{OperatorToken: token}, "", false},
		{"wrong token is refused", config.AuthConfig{OperatorToken: token, Production: true}, "nope", false},
		{"right token passes", config.AuthConfig{OperatorToken: token, Production: true}, token, true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", nil)
		if c.header != "" {
			req.Header.Set(operatorHeader, c.header)
		}
		if err := apiWith(c.auth).requireOperator(req); (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestRevokeNeedsTheResourcesOwnTokenOrTheOperator(t *testing.T) {
	a := apiWith(config.AuthConfig{Production: true})
	for _, path := range []string{"/api/v1/tenants/ten_123/revoke", "/api/v1/integrators/int_123/revoke"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		switch {
		case strings.Contains(path, "tenants"):
			req.SetPathValue("id", "ten_123")
			a.revokeTenant(rec, req)
		default:
			req.SetPathValue("id", "int_123")
			a.revokeIntegrator(rec, req)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token: status %d, want 401 (knowing an ID must not be enough)", path, rec.Code)
		}
	}
}

func TestCreateTenantClosedInProductionWithoutOperatorToken(t *testing.T) {
	a := apiWith(config.AuthConfig{Production: true})
	rec := httptest.NewRecorder()
	a.createTenant(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tenants", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

func TestStartDemo_CappedPerAddress(t *testing.T) {
	a := &API{
		b:       &wiring.Bundle{AuthConfig: config.AuthConfig{DemoAccounts: true, DemoAccountsPerIPPerDay: 5}, Demo: &app.DemoService{}},
		limiter: newFakeLimiter(0), // already at the cap
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/demo", nil)
	req.RemoteAddr = "198.51.100.7:4000"
	rec := httptest.NewRecorder()
	a.startDemo(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("status %d, Retry-After %q — want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestClientIP_IgnoresForgedForwardedFor(t *testing.T) {
	defer func(h int) { trustedProxyHops = h }(trustedProxyHops)
	cases := []struct {
		hops   int
		xff    []string
		remote string
		want   string
	}{
		// One load balancer appends the client it saw; a forged value the
		// client sent sits to the left and is ignored.
		{1, []string{"6.6.6.6, 203.0.113.9"}, "10.0.0.2:5000", "203.0.113.9"},
		// Google Cloud's LB appends client + its own address; header split across lines.
		{2, []string{"6.6.6.6", "198.51.100.4, 10.0.0.7"}, "10.0.0.2:5000", "198.51.100.4"},
		// Fewer entries than proxies (API reached directly): the socket peer.
		{2, []string{"6.6.6.6"}, "192.0.2.1:4000", "192.0.2.1"},
		{0, []string{"6.6.6.6"}, "192.0.2.1:4000", "192.0.2.1"},
	}
	for _, c := range cases {
		trustedProxyHops = c.hops
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		for _, v := range c.xff {
			req.Header.Add("X-Forwarded-For", v)
		}
		if got := clientIP(req); got != c.want {
			t.Errorf("hops=%d xff=%v: got %q, want %q", c.hops, c.xff, got, c.want)
		}
		if got := rateLimitKey(req); got != "ip:"+c.want {
			t.Errorf("rate-limit key %q should use the trusted client IP %q", got, c.want)
		}
	}
}
