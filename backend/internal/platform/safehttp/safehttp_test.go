package safehttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"10.0.0.1", "172.16.5.4", "192.168.1.1", // RFC 1918
		"169.254.169.254",    // cloud metadata
		"100.64.0.1",         // carrier-grade NAT
		"0.0.0.0", "0.1.2.3", // "this" network
		"224.0.0.1", "239.255.255.250", // multicast
		"240.0.0.1", "255.255.255.255", // reserved, broadcast
		"192.0.0.8", "198.18.0.1", // protocol assignments, benchmarking
		"192.0.2.1", "203.0.113.9", // documentation
		"127.0.0.1", "::1", // loopback (blocked unless allowed)
		"fc00::1", "fd12:3456::1", // unique local
		"fe80::1",       // link-local
		"::", "ff02::1", // unspecified, multicast
		"::ffff:10.0.0.1",        // IPv4-mapped private
		"::ffff:169.254.169.254", // IPv4-mapped metadata
		"64:ff9b::a00:1",         // NAT64
		"2001:db8::1",            // documentation
	}
	for _, s := range blocked {
		if !BlockedIP(netip.MustParseAddr(s), false) {
			t.Errorf("%s must be blocked", s)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "::ffff:8.8.8.8"}
	for _, s := range public {
		if BlockedIP(netip.MustParseAddr(s), false) {
			t.Errorf("%s is public and must be allowed", s)
		}
	}
	if BlockedIP(netip.MustParseAddr("127.0.0.1"), true) || BlockedIP(netip.MustParseAddr("::1"), true) {
		t.Error("loopback is allowed when the client allows it")
	}
	if !BlockedIP(netip.MustParseAddr("10.0.0.1"), true) {
		t.Error("allowing loopback must not allow private ranges")
	}
	if !BlockedIP(netip.Addr{}, false) {
		t.Error("an invalid address is blocked")
	}
}

func get(t *testing.T, c *Client, url string) (*Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

func TestLoopbackIsBlockedByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer srv.Close()

	_, err := get(t, New(Options{}), srv.URL)
	if err == nil {
		t.Fatal("a default client must not reach a loopback server")
	}
	// Plain http is refused before anything is dialled.
	if !strings.Contains(err.Error(), "plain http") {
		t.Errorf("want the scheme refusal, got %v", err)
	}

	resp, err := get(t, New(Options{AllowLoopback: true}), srv.URL)
	if err != nil || resp.Status != 200 || string(resp.Body) != "hi" {
		t.Fatalf("a loopback-allowed client reaches it: %v %+v", err, resp)
	}
}

// The sandbox provider lives on this machine; nothing else on it may be
// reachable. Loopback is allowed on named ports only.
func TestLoopbackPortsAllowOnlyTheNamedPorts(t *testing.T) {
	sandbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("sandbox")) }))
	defer sandbox.Close()
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("admin panel")) }))
	defer internal.Close()

	port := func(srv *httptest.Server) int {
		ap, err := netip.ParseAddrPort(strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		return int(ap.Port())
	}
	c := New(Options{LoopbackPorts: []int{port(sandbox)}})

	resp, err := get(t, c, sandbox.URL)
	if err != nil || string(resp.Body) != "sandbox" {
		t.Fatalf("the named port is reachable: %v", err)
	}
	if _, err := get(t, c, internal.URL); err == nil {
		t.Fatal("another service on localhost must not be reachable")
	}
	// The IP-literal and the name for the same loopback are both held to the port list.
	if _, err := get(t, c, strings.Replace(internal.URL, "127.0.0.1", "localhost", 1)); err == nil {
		t.Fatal("localhost by name is no different")
	}
	// Plain http to a public host is still refused.
	if _, err := get(t, c, "http://example.com/"); err == nil {
		t.Error("plain http to a public host stays refused")
	}
}

func TestUrlPort(t *testing.T) {
	for raw, want := range map[string]uint16{
		"http://localhost/x": 80, "https://localhost/x": 443, "http://localhost:8080/x": 8080, "https://[::1]:9443/": 9443,
	} {
		u, _ := url.Parse(raw)
		if got := urlPort(u); got != want {
			t.Errorf("urlPort(%s) = %d, want %d", raw, got, want)
		}
	}
}

// A hostname that resolves to a blocked address must be refused at connect
// time, not trusted because its name looks fine.
func TestResolvedAddressIsChecked(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer srv.Close()
	// httptest binds 127.0.0.1; reach it by a name that resolves there.
	url := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	_, err := get(t, New(Options{}), url)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("localhost resolves to loopback and must be blocked at dial time, got %v", err)
	}
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()

	resp, err := get(t, New(Options{AllowLoopback: true}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusFound || resp.Header.Get("Location") != other.URL {
		t.Errorf("the 3xx should come back as is: %d %q", resp.Status, resp.Header.Get("Location"))
	}
	if hit {
		t.Error("the redirect target must never be contacted")
	}
}

func TestBodyLimitFailsInsteadOfTruncating(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 2048)))
	}))
	defer srv.Close()

	if _, err := get(t, New(Options{AllowLoopback: true, MaxBody: 1024}), srv.URL); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("an oversized body must fail, got %v", err)
	}
	resp, err := get(t, New(Options{AllowLoopback: true, MaxBody: 2048}), srv.URL)
	if err != nil || len(resp.Body) != 2048 {
		t.Fatalf("a body exactly at the limit is fine: %v", err)
	}
}

func TestRefusesPlainHTTPToPublicHostsAndCredentials(t *testing.T) {
	c := New(Options{AllowLoopback: true})
	for _, u := range []string{"http://example.com/x", "ftp://example.com/x", "https://user:pw@example.com/x", "file:///etc/passwd"} {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Do(req); err == nil {
			t.Errorf("%s must be refused before any connection", u)
		}
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()
	if _, err := get(t, New(Options{AllowLoopback: true, Timeout: 50 * time.Millisecond}), srv.URL); err == nil {
		t.Fatal("a slow provider must time out")
	}
}

func TestUserAgentIsSet(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ua = r.Header.Get("User-Agent") }))
	defer srv.Close()
	if _, err := get(t, New(Options{AllowLoopback: true}), srv.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ua, "algebra-executor/") {
		t.Errorf("user agent = %q", ua)
	}
}
