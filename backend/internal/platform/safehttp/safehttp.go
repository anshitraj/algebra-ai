// Package safehttp is the HTTP client Algebra uses to call providers it
// doesn't control, including ones found on the open web, and pays.
//
// A provider URL is attacker-influenced input that Algebra then sends money
// and payment credentials to, so the client is deliberately narrow:
//
//   - It refuses to connect to private, loopback, link-local, multicast and
//     other non-public addresses. The check runs on the address actually being
//     dialled, after DNS, so a hostname that resolves to 169.254.169.254 (or
//     is rebound there between a check and a connect) is refused.
//   - It never follows redirects. A paid request carries a payment header; a
//     3xx to another host must not be allowed to carry it along.
//   - It ignores proxy environment variables, which would bypass the address
//     check.
//   - It reads a bounded body and fails, rather than truncating, when the
//     body is larger: a truncated body hashes to something the provider never
//     sent.
package safehttp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Defaults.
const (
	DefaultTimeout = 20 * time.Second
	DefaultMaxBody = 1 << 20
)

// Options configure a Client.
type Options struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1 on any port, over plain http
	// or https: for tests only. Anything an agent can point at must use
	// LoopbackPorts instead, or it could reach every service on the host.
	AllowLoopback bool
	// LoopbackPorts permits loopback addresses on these ports only: the port
	// the sandbox provider is served on, and nothing else on the machine.
	LoopbackPorts []int
	// Timeout bounds one whole request, dial to last byte.
	Timeout time.Duration
	// MaxBody bounds a response body in bytes.
	MaxBody int64
	// Resolver overrides DNS resolution (tests).
	Resolver *net.Resolver
}

// ErrBlockedAddress: the destination isn't a public address.
var ErrBlockedAddress = errors.New("safehttp: refusing to connect to a non-public address")

// ErrBodyTooLarge: the response body exceeds the client's limit.
var ErrBodyTooLarge = errors.New("safehttp: response body is larger than the limit")

// Response is a fully read response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client makes bounded requests to untrusted hosts.
type Client struct {
	http    *http.Client
	maxBody int64
	opts    Options
}

// New builds a Client.
func New(o Options) *Client {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxBody <= 0 {
		o.MaxBody = DefaultMaxBody
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Resolver: o.Resolver,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %q", ErrBlockedAddress, address)
			}
			if BlockedIP(ap.Addr(), o.loopbackOK(ap.Port())) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, ap.Addr())
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Client{
		maxBody: o.MaxBody, opts: o,
		http: &http.Client{
			Transport: tr,
			Timeout:   o.Timeout,
			// Never follow: see the package comment.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Do sends req and returns the whole response. Only https is allowed, and
// plain http only for loopback when the client permits it.
func (c *Client) Do(req *http.Request) (*Response, error) {
	switch req.URL.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(req.URL.Hostname()) || !c.opts.loopbackOK(urlPort(req.URL)) {
			return nil, errors.New("safehttp: plain http is refused (https only)")
		}
	default:
		return nil, fmt.Errorf("safehttp: unsupported scheme %q", req.URL.Scheme)
	}
	if req.URL.User != nil {
		return nil, errors.New("safehttp: URL must not carry credentials")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "algebra-executor/1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("safehttp: reading response: %w", err)
	}
	if int64(len(body)) > c.maxBody {
		return nil, ErrBodyTooLarge
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// loopbackOK reports whether a loopback destination on port is permitted.
func (o Options) loopbackOK(port uint16) bool {
	return o.AllowLoopback || slices.Contains(o.LoopbackPorts, int(port))
}

// urlPort is the port a URL connects to, defaulting by scheme.
func urlPort(u *url.URL) uint16 {
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return 0
		}
		return uint16(n)
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

// CloseIdleConnections releases pooled connections.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// isLoopbackHost reports whether a URL host is loopback by name or literal.
func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Unmap().IsLoopback()
}

// reserved are public-looking ranges that aren't public hosts. The netip
// predicates cover private, loopback, link-local, multicast and unspecified;
// these are the rest.
var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this" network
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 can reach IPv4 private space
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// BlockedIP reports whether Algebra must not connect to ip. IPv4-mapped IPv6
// addresses are judged as the IPv4 address they wrap.
func BlockedIP(ip netip.Addr, allowLoopback bool) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	if ip.IsLoopback() {
		return !allowLoopback
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range reserved {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
