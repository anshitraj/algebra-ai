package remotemcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// LoginOptions configures an interactive account link.
type LoginOptions struct {
	// RedirectURL is the loopback callback, e.g.
	// http://127.0.0.1:8765/callback. Merchants allowlist loopback redirects
	// for native clients (RFC 8252 §7.3): Zepto documents http://127.0.0.1
	// and http://localhost (with /callback variants); Swiggy allows
	// http://localhost for development.
	RedirectURL string
	// ShowURL receives the authorization URL. The user opens it in their
	// own browser and completes the merchant's login (phone + OTP) there.
	// Required.
	ShowURL    func(authURL string)
	ClientName string
	// Timeout bounds the whole flow, including the time the user takes.
	// Defaults to 10 minutes.
	Timeout    time.Duration
	HTTPClient *http.Client
	// AllowInsecureLoopback admits loopback metadata/token URLs. Tests only.
	AllowInsecureLoopback bool
	Now                   func() time.Time
}

const maxResponseBytes = 1 << 20

// Login links a merchant account using the OAuth 2.1 authorization-code
// flow the MCP authorization spec prescribes: protected-resource metadata
// discovery (RFC 9728) → authorization-server metadata (RFC 8414) → dynamic
// client registration (RFC 7591) → PKCE S256 + state + resource indicator
// (RFC 8707) → loopback redirect → code exchange.
//
// It requests every scope the resource server advertises rather than only
// the one named in its first 401 challenge: Zepto's challenge asks for
// "tools:read", but search-to-order needs its write scopes too, and a
// non-interactive server process cannot do step-up authorization later.
//
// The returned Session is not saved; the caller decides.
func Login(ctx context.Context, merchant, endpoint string, opts LoginOptions) (*Session, error) {
	if !merchantNameRE.MatchString(merchant) {
		return nil, fmt.Errorf("remotemcp: invalid merchant name %q", merchant)
	}
	if err := ValidateEndpoint(endpoint, opts.AllowInsecureLoopback); err != nil {
		return nil, err
	}
	if opts.ShowURL == nil {
		return nil, errors.New("remotemcp: LoginOptions.ShowURL is required")
	}
	listenAddr, callbackPath, err := parseLoopbackRedirect(opts.RedirectURL)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	clientName := opts.ClientName
	if clientName == "" {
		clientName = "Project Algebra"
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prm, err := discoverResource(ctx, hc, endpoint, opts.AllowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	resource := endpoint
	if prm.Resource != "" {
		if !sameResource(prm.Resource, endpoint) {
			return nil, fmt.Errorf("remotemcp: protected-resource metadata names resource %q, not %q; refusing to link", prm.Resource, endpoint)
		}
		resource = prm.Resource
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("remotemcp: %s advertises no authorization server", endpoint)
	}
	issuer := prm.AuthorizationServers[0]
	if err := ValidateEndpoint(issuer, opts.AllowInsecureLoopback); err != nil {
		return nil, err
	}
	asm, err := auth.GetAuthServerMetadata(ctx, issuer, hc)
	if err != nil {
		return nil, fmt.Errorf("remotemcp: fetching authorization server metadata: %w", err)
	}
	if asm == nil {
		return nil, fmt.Errorf("remotemcp: authorization server %s publishes no metadata", issuer)
	}
	for _, u := range []string{asm.AuthorizationEndpoint, asm.TokenEndpoint} {
		if err := ValidateEndpoint(u, opts.AllowInsecureLoopback); err != nil {
			return nil, err
		}
	}
	if !slices.Contains(asm.CodeChallengeMethodsSupported, "S256") {
		return nil, fmt.Errorf("remotemcp: %s does not advertise PKCE S256; refusing to authorize without it", issuer)
	}

	scopes := slices.Clone(prm.ScopesSupported)
	if len(scopes) == 0 {
		scopes = slices.Clone(asm.ScopesSupported)
	}
	if slices.Contains(asm.ScopesSupported, "offline_access") && !slices.Contains(scopes, "offline_access") {
		scopes = append(scopes, "offline_access")
	}

	reg, err := registerClient(ctx, hc, asm, opts.RedirectURL, scopes, clientName, opts.AllowInsecureLoopback)
	if err != nil {
		return nil, err
	}

	oc := &oauth2.Config{
		ClientID:     reg.clientID,
		ClientSecret: reg.clientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: asm.AuthorizationEndpoint, TokenURL: asm.TokenEndpoint, AuthStyle: reg.authStyle},
		RedirectURL:  opts.RedirectURL,
		Scopes:       scopes,
	}
	verifier := oauth2.GenerateVerifier()
	state := rand.Text()
	authURL := oc.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", resource))

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("remotemcp: listening for the OAuth redirect on %s: %w", listenAddr, err)
	}
	results := make(chan callbackResult, 1)
	srv := &http.Server{Handler: callbackHandler(callbackPath, results), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	opts.ShowURL(authURL)

	var res callbackResult
	select {
	case res = <-results:
	case <-ctx.Done():
		return nil, fmt.Errorf("remotemcp: timed out waiting for the browser redirect: %w", ctx.Err())
	}
	if res.err != "" {
		return nil, fmt.Errorf("remotemcp: authorization was not granted (%s)", res.err)
	}
	if subtle.ConstantTimeCompare([]byte(res.state), []byte(state)) != 1 {
		return nil, errors.New("remotemcp: OAuth state mismatch; aborting (possible cross-site request forgery)")
	}
	if err := validateIssuerResponse(res.iss, asm.Issuer, asm.AuthorizationResponseIssParameterSupported); err != nil {
		return nil, err
	}

	exchangeCtx := context.WithValue(ctx, oauth2.HTTPClient, hc)
	tok, err := oc.Exchange(exchangeCtx, res.code, oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", resource))
	if err != nil {
		return nil, fmt.Errorf("remotemcp: token exchange failed: %w", err)
	}

	return &Session{
		Merchant:     merchant,
		Endpoint:     endpoint,
		Issuer:       asm.Issuer,
		ClientID:     reg.clientID,
		ClientSecret: reg.clientSecret,
		AuthURL:      asm.AuthorizationEndpoint,
		TokenURL:     asm.TokenEndpoint,
		AuthStyle:    reg.authStyle,
		Scopes:       scopes,
		Resource:     resource,
		Token:        tok,
		LinkedAt:     now().UTC(),
	}, nil
}

// parseLoopbackRedirect accepts only http://127.0.0.1:<port>/path,
// http://localhost:<port>/path or http://[::1]:<port>/path, and returns the
// loopback address to listen on — never a wildcard interface.
func parseLoopbackRedirect(raw string) (listenAddr, path string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return "", "", fmt.Errorf("remotemcp: redirect URL %q must be an http loopback URL (RFC 8252 §7.3)", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", "", fmt.Errorf("remotemcp: redirect URL %q must not carry a query, fragment, or credentials", raw)
	}
	port := u.Port()
	if port == "" {
		return "", "", fmt.Errorf("remotemcp: redirect URL %q needs an explicit port", raw)
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost":
		listenAddr = "127.0.0.1:" + port
	case "::1":
		listenAddr = "[::1]:" + port
	default:
		return "", "", fmt.Errorf("remotemcp: redirect URL %q is not a loopback address", raw)
	}
	path = u.Path
	if path == "" {
		path = "/"
	}
	return listenAddr, path, nil
}

type callbackResult struct {
	code, state, iss, err string
}

var oauthErrorCodeRE = regexp.MustCompile(`^[a-z_]{1,64}$`)

// callbackHandler receives the one browser redirect. The page it returns is
// static: nothing from the query string is reflected back.
func callbackHandler(path string, out chan<- callbackResult) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		res := callbackResult{code: q.Get("code"), state: q.Get("state"), iss: q.Get("iss")}
		if e := q.Get("error"); e != "" {
			res.err = "authorization error"
			if oauthErrorCodeRE.MatchString(e) {
				res.err = e
			}
		} else if res.code == "" {
			res.err = "no authorization code in redirect"
		}
		delivered := false
		once.Do(func() {
			out <- res
			delivered = true
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		switch {
		case !delivered:
			_, _ = io.WriteString(w, callbackPage("This link was already used. You can close this tab."))
		case res.err != "":
			_, _ = io.WriteString(w, callbackPage("Linking did not complete. Return to your terminal for details."))
		default:
			_, _ = io.WriteString(w, callbackPage("Account linked. You can close this tab and return to your terminal."))
		}
	})
}

func callbackPage(msg string) string {
	return "<!doctype html><meta charset=utf-8><title>Project Algebra</title><p style=\"font-family:system-ui;margin:3rem\">" + msg + "</p>"
}

// discoverResource finds the protected-resource metadata: first from the
// server's own 401 challenge, then from the RFC 9728 well-known locations.
func discoverResource(ctx context.Context, hc *http.Client, endpoint string, allowLoopback bool) (*oauthex.ProtectedResourceMetadata, error) {
	var candidates []string
	if u := challengeMetadataURL(ctx, hc, endpoint); u != "" {
		candidates = append(candidates, u)
	}
	candidates = append(candidates, wellKnownResourceMetadataURLs(endpoint)...)

	var lastErr error
	for _, u := range candidates {
		if err := ValidateEndpoint(u, allowLoopback); err != nil {
			lastErr = err
			continue
		}
		var prm oauthex.ProtectedResourceMetadata
		if err := getJSON(ctx, hc, u, &prm); err != nil {
			lastErr = err
			continue
		}
		return &prm, nil
	}
	return nil, fmt.Errorf("remotemcp: could not discover OAuth protected-resource metadata for %s: %v", endpoint, lastErr)
}

var resourceMetadataParamRE = regexp.MustCompile(`resource_metadata="([^"]+)"`)

// challengeMetadataURL sends an unauthenticated initialize request and reads
// resource_metadata from the WWW-Authenticate challenge, if any.
func challengeMetadataURL(ctx context.Context, hc *http.Client, endpoint string) string {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"project-algebra","version":"0.1.0"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := noRedirects(hc).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode != http.StatusUnauthorized {
		return ""
	}
	for _, h := range resp.Header.Values("WWW-Authenticate") {
		if m := resourceMetadataParamRE.FindStringSubmatch(h); m != nil {
			return m[1]
		}
	}
	return ""
}

func wellKnownResourceMetadataURLs(endpoint string) []string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil
	}
	origin := u.Scheme + "://" + u.Host
	var out []string
	if p := strings.TrimSuffix(u.Path, "/"); p != "" {
		out = append(out, origin+"/.well-known/oauth-protected-resource"+p)
		// Not RFC 9728, but a common MCP-server convention — and where
		// Swiggy actually serves it (its challenge points at the origin
		// root, which 404s). Same origin as the endpoint, so no new trust.
		out = append(out, origin+p+"/.well-known/oauth-protected-resource")
	}
	return append(out, origin+"/.well-known/oauth-protected-resource")
}

// sameResource accepts the endpoint itself or its origin as the advertised
// resource identifier.
func sameResource(advertised, endpoint string) bool {
	a := strings.TrimSuffix(advertised, "/")
	e := strings.TrimSuffix(endpoint, "/")
	if a == e {
		return true
	}
	u, err := url.Parse(endpoint)
	return err == nil && a == u.Scheme+"://"+u.Host
}

type registration struct {
	clientID     string
	clientSecret string
	authStyle    oauth2.AuthStyle
}

// registerClient performs RFC 7591 dynamic registration, preferring a public
// client (token_endpoint_auth_method "none") so there is no client secret to
// protect at all.
func registerClient(ctx context.Context, hc *http.Client, asm *oauthex.AuthServerMeta, redirect string, scopes []string, clientName string, allowLoopback bool) (*registration, error) {
	if asm.RegistrationEndpoint == "" {
		return nil, fmt.Errorf("remotemcp: %s does not support dynamic client registration", asm.Issuer)
	}
	if err := ValidateEndpoint(asm.RegistrationEndpoint, allowLoopback); err != nil {
		return nil, err
	}
	method := "none"
	if supported := asm.TokenEndpointAuthMethodsSupported; len(supported) > 0 && !slices.Contains(supported, "none") {
		switch {
		case slices.Contains(supported, "client_secret_post"):
			method = "client_secret_post"
		case slices.Contains(supported, "client_secret_basic"):
			method = "client_secret_basic"
		default:
			return nil, fmt.Errorf("remotemcp: %s supports no usable token endpoint auth method", asm.Issuer)
		}
	}
	meta := oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{redirect},
		TokenEndpointAuthMethod: method,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              clientName,
		Scope:                   strings.Join(scopes, " "),
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("remotemcp: encoding client registration: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, asm.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := noRedirects(hc).Do(req)
	if err != nil {
		return nil, fmt.Errorf("remotemcp: client registration: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("remotemcp: reading client registration response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remotemcp: client registration rejected (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var out struct {
		ClientID                string `json:"client_id"`
		ClientSecret            string `json:"client_secret"`
		TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ClientID == "" {
		return nil, errors.New("remotemcp: client registration response has no client_id")
	}
	if out.TokenEndpointAuthMethod != "" {
		method = out.TokenEndpointAuthMethod
	}
	style := oauth2.AuthStyleInParams
	if method == "client_secret_basic" {
		style = oauth2.AuthStyleInHeader
	}
	return &registration{clientID: out.ClientID, clientSecret: out.ClientSecret, authStyle: style}, nil
}

// validateIssuerResponse applies RFC 9207's mix-up defense.
func validateIssuerResponse(iss, expected string, supported bool) error {
	switch {
	case supported && iss == "":
		return errors.New("remotemcp: authorization server advertises RFC 9207 iss but the redirect carried none")
	case supported && iss != expected:
		return fmt.Errorf("remotemcp: redirect issuer %q does not match %q", iss, expected)
	case !supported && iss != "" && iss != expected:
		return fmt.Errorf("remotemcp: redirect issuer %q does not match %q", iss, expected)
	}
	return nil
}

func getJSON(ctx context.Context, hc *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := noRedirects(hc).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out)
}

// noRedirects copies hc with redirects disabled: metadata and registration
// requests must be answered by the host that was validated, not wherever it
// forwards to.
func noRedirects(hc *http.Client) *http.Client {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}
