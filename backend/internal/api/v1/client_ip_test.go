package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testEdgeSecret = "0123456789abcdef0123456789abcdef"

// Behind the web app the API sees the web host's address for everyone. The web
// app's proxy sends the visitor's address with a shared secret; without that
// secret the header is just a header anyone can forge, and is never read.
func TestClientIP_EdgeHeaderNeedsTheSharedSecret(t *testing.T) {
	defer func(h int, s []byte) { trustedProxyHops, edgeSecret = h, s }(trustedProxyHops, edgeSecret)
	trustedProxyHops = 2

	const viaForwardedFor = "9.9.9.9" // what X-Forwarded-For alone gives: the web host
	cases := []struct {
		name      string
		configure string // the API's EDGE_SHARED_SECRET ("" = not set)
		secret    string // X-Algebra-Edge-Secret sent ("" = none)
		ip        string // X-Algebra-Client-IP sent ("" = none)
		want      string
	}{
		{"secret and address: the visitor", testEdgeSecret, testEdgeSecret, "198.51.100.23", "198.51.100.23"},
		{"an IPv6 visitor", testEdgeSecret, testEdgeSecret, "2001:db8::1", "2001:db8::1"},
		{"IPv6 is written one way", testEdgeSecret, testEdgeSecret, "2001:DB8:0:0:0:0:0:1", "2001:db8::1"},
		{"an IPv4-mapped address is IPv4", testEdgeSecret, testEdgeSecret, "::ffff:198.51.100.23", "198.51.100.23"},
		{"a zone is dropped", testEdgeSecret, testEdgeSecret, "fe80::1%eth0", "fe80::1"},
		{"wrong secret: the header is ignored", testEdgeSecret, "x" + testEdgeSecret[1:], "198.51.100.23", viaForwardedFor},
		{"a prefix of the secret is not the secret", testEdgeSecret, testEdgeSecret[:16], "198.51.100.23", viaForwardedFor},
		{"no secret sent: ignored", testEdgeSecret, "", "198.51.100.23", viaForwardedFor},
		{"secret but no address", testEdgeSecret, testEdgeSecret, "", viaForwardedFor},
		{"secret but not an address", testEdgeSecret, testEdgeSecret, "not-an-ip", viaForwardedFor},
		{"secret but a list, not one address", testEdgeSecret, testEdgeSecret, "198.51.100.23, 6.6.6.6", viaForwardedFor},
		{"the API has no secret: headers are never read", "", testEdgeSecret, "198.51.100.23", viaForwardedFor},
		{"the API has no secret and an empty one is sent", "", "", "198.51.100.23", viaForwardedFor},
	}
	for _, c := range cases {
		edgeSecret = []byte(c.configure)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "100.64.0.1:5000"
		req.Header.Set("X-Forwarded-For", viaForwardedFor+", 100.64.0.9")
		if c.secret != "" {
			req.Header.Set(edgeSecretHeader, c.secret)
		}
		if c.ip != "" {
			req.Header.Set(edgeClientIPHeader, c.ip)
		}
		if got := clientIP(req); got != c.want {
			t.Errorf("%s: clientIP = %q, want %q", c.name, got, c.want)
		}
		if got := clientMeta(req).IP; got != c.want {
			t.Errorf("%s: clientMeta IP = %q, want %q", c.name, got, c.want)
		}
		if got := rateLimitKey(req); got != "ip:"+c.want {
			t.Errorf("%s: rate-limit key = %q, want ip:%s", c.name, got, c.want)
		}
	}
}

// Two visitors behind the same web host get two buckets, which is the point.
func TestRateLimitKey_SeparatesVisitorsBehindTheWebHost(t *testing.T) {
	defer func(s []byte) { edgeSecret = s }(edgeSecret)
	edgeSecret = []byte(testEdgeSecret)
	key := func(ip string) string {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "100.64.0.1:5000"
		req.Header.Set("X-Forwarded-For", "9.9.9.9, 100.64.0.9")
		req.Header.Set(edgeSecretHeader, testEdgeSecret)
		req.Header.Set(edgeClientIPHeader, ip)
		return rateLimitKey(req)
	}
	a, b := key("198.51.100.23"), key("203.0.113.8")
	if a == b {
		t.Fatalf("two visitors share the bucket %q", a)
	}
	if key("198.51.100.23") != a {
		t.Fatalf("one visitor must keep one bucket")
	}
}
