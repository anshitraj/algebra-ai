package merchant

import "testing"

func TestAllowedDomains_ExactAndSubdomainMatch(t *testing.T) {
	a := NewAllowedDomains("zepto.co.in", "amazon.in")

	cases := []struct {
		url     string
		wantErr bool
	}{
		{"https://zepto.co.in/checkout", false},
		{"https://mcp.zepto.co.in/mcp", false}, // subdomain
		{"https://amazon.in/cart", false},
		{"https://evil.com/zepto.co.in", true}, // host is evil.com, not a subdomain
		{"https://notzepto.co.in/", true},      // NOT a subdomain (no dot boundary) — different host entirely
		{"http://zepto.co.in/checkout", true},  // http, not https
		{"https://", true},                     // no host
		{"not a url at all", true},
	}
	for _, c := range cases {
		err := a.ValidateURL(c.url)
		if c.wantErr && err == nil {
			t.Errorf("ValidateURL(%q): expected error, got nil", c.url)
		}
		if !c.wantErr && err != nil {
			t.Errorf("ValidateURL(%q): unexpected error: %v", c.url, err)
		}
	}
}

// TestAllowedDomains_RejectsPrivateAndLoopbackHosts is the SSRF guard that
// must hold even if an operator widens the allowlist carelessly — a
// merchant-supplied URL must never be able to point Algebra (or an agent,
// or a future browser executor) at localhost, a private subnet, or a cloud
// metadata endpoint.
func TestAllowedDomains_RejectsPrivateAndLoopbackHosts(t *testing.T) {
	// Deliberately allowlist the private ranges' hostnames to prove the
	// private-address check is independent of the allowlist.
	a := NewAllowedDomains("localhost", "127.0.0.1", "169.254.169.254", "10.0.0.5", "192.168.1.1")

	for _, raw := range []string{
		"https://localhost/checkout",
		"https://127.0.0.1/admin",
		"https://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"https://10.0.0.5/internal",
		"https://192.168.1.1/router",
		"https://0.0.0.0/",
	} {
		if err := a.ValidateURL(raw); err == nil {
			t.Errorf("expected %q to be rejected as a private/loopback/link-local target", raw)
		}
	}
}

func TestSanitizeProductURL(t *testing.T) {
	a := NewAllowedDomains("zepto.co.in")

	if got := a.SanitizeProductURL("https://www.zepto.co.in/p/coke-zero"); got != "https://www.zepto.co.in/p/coke-zero" {
		t.Errorf("expected an allowlisted merchant URL to pass through, got %q", got)
	}
	if got := a.SanitizeProductURL("https://169.254.169.254/latest/meta-data/"); got != "" {
		t.Errorf("expected a metadata-endpoint URL to be blanked, got %q", got)
	}
	if got := a.SanitizeProductURL("https://evil.example/pwn"); got != "" {
		t.Errorf("expected a non-allowlisted URL to be blanked, got %q", got)
	}
	if got := a.SanitizeProductURL(""); got != "" {
		t.Errorf("expected an empty URL to stay empty, got %q", got)
	}
}

func TestAllowedDomains_RejectsUnlistedHost(t *testing.T) {
	a := NewAllowedDomains("zepto.co.in")
	if err := a.ValidateURL("https://attacker.example/steal"); err == nil {
		t.Error("expected an unlisted host to be rejected")
	}
}
