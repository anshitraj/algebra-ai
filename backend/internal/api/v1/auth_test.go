package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

func TestSafeNext_OnlySameSitePaths(t *testing.T) {
	ok := map[string]string{"/console/orders": "/console/orders", "/console?x=1": "/console?x=1"}
	for in, want := range ok {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "https://evil.example", "//evil.example", "/\\evil.example", "console"} {
		if got := safeNext(bad); got != "" {
			t.Errorf("safeNext(%q) = %q — open redirect", bad, got)
		}
	}
}

func TestOAuthState_SignedAndTamperEvident(t *testing.T) {
	a := &API{b: &wiring.Bundle{OAuthStateKey: []byte("test-key-32-bytes-long-enough!!!")}}
	signed := a.signState("github|state|verifier|123|/console")
	payload, ok := a.verifyState(signed)
	if !ok || payload != "github|state|verifier|123|/console" {
		t.Fatalf("round trip failed: %q %v", payload, ok)
	}
	// Swap provider inside the payload, keep the signature.
	dot := strings.LastIndexByte(signed, '.')
	forged := (&API{b: &wiring.Bundle{OAuthStateKey: []byte("other-key")}}).signState("google|state|verifier|123|/console")
	if _, ok := a.verifyState(forged[:strings.LastIndexByte(forged, '.')] + signed[dot:]); ok {
		t.Error("tampered state accepted")
	}
	if _, ok := (&API{b: &wiring.Bundle{OAuthStateKey: []byte("different")}}).verifyState(signed); ok {
		t.Error("state signed with another key accepted")
	}
}

func TestCSRFGuard_RejectsForeignOriginWithSessionCookie(t *testing.T) {
	a := &API{
		b:              &wiring.Bundle{AuthConfig: config.AuthConfig{PublicWebURL: "https://app.example"}},
		allowedOrigins: map[string]bool{},
	}
	h := a.csrfGuard(okHandler())
	cases := []struct {
		origin string
		cookie bool
		method string
		want   int
	}{
		{"https://evil.example", true, http.MethodPost, http.StatusForbidden},
		{"https://app.example", true, http.MethodPost, http.StatusOK},
		{"", true, http.MethodPost, http.StatusOK},                      // server-to-server, no ambient credentials
		{"https://evil.example", false, http.MethodPost, http.StatusOK}, // no cookie → nothing to forge
		{"https://evil.example", true, http.MethodGet, http.StatusOK},   // safe method
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/api/v1/me/onboarding", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if c.cookie {
			req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "x"})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s origin=%q cookie=%v: got %d, want %d", c.method, c.origin, c.cookie, rec.Code, c.want)
		}
	}
}
