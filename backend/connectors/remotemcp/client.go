package remotemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/project-algebra/algebra/connectors/sanitize"
	"golang.org/x/oauth2"
)

// ErrSessionExpired means the merchant rejected the linked credentials
// (HTTP 401/403) or they could not be refreshed. Only the user can fix this,
// by re-linking the account.
var ErrSessionExpired = errors.New("remotemcp: merchant session expired, revoked, or missing scopes; re-link the account")

// ErrInputRequired means the merchant server asked for interactive input in
// the middle of a tool call. Algebra never answers those on the user's
// behalf.
var ErrInputRequired = errors.New("remotemcp: merchant server requested interactive input")

// ToolError is a failure the merchant's own tool reported. Message is the
// merchant's text, truncated.
type ToolError struct {
	Tool    string
	Message string
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("remotemcp: merchant tool %s failed: %s", e.Tool, e.Message)
}

// Config configures a Client for one merchant's MCP server.
type Config struct {
	Merchant string
	Endpoint string
	Store    SessionStore
	// HTTPClient is the unauthenticated base client used for MCP calls and
	// token refreshes. Defaults to a client with a 30s timeout.
	HTTPClient *http.Client
	// AllowInsecureLoopback permits http:// endpoints on loopback. Tests
	// only — a real merchant endpoint must be https on a public host.
	AllowInsecureLoopback bool
}

// Client calls one merchant's MCP server with the linked session. Safe for
// concurrent use; the underlying MCP session is created lazily and recreated
// after a connection loss.
type Client struct {
	cfg  Config
	base *http.Client

	// authFailed is set by the HTTP layer when the merchant answers 401/403
	// or a token refresh fails, so a transport error can be reported as
	// "re-link the account" instead of an opaque network failure.
	authFailed atomic.Bool

	mu      sync.Mutex
	session *mcp.ClientSession
	tools   map[string]*mcp.Tool
}

func NewClient(cfg Config) (*Client, error) {
	if !merchantNameRE.MatchString(cfg.Merchant) {
		return nil, fmt.Errorf("remotemcp: invalid merchant name %q", cfg.Merchant)
	}
	if err := ValidateEndpoint(cfg.Endpoint, cfg.AllowInsecureLoopback); err != nil {
		return nil, err
	}
	if cfg.Store == nil {
		return nil, errors.New("remotemcp: a SessionStore is required")
	}
	base := cfg.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, base: base}, nil
}

func (c *Client) Merchant() string { return c.cfg.Merchant }
func (c *Client) Endpoint() string { return c.cfg.Endpoint }

// LoadSession returns the linked session, or ErrNotLinked.
func (c *Client) LoadSession() (*Session, error) {
	sess, err := c.cfg.Store.Load(c.cfg.Merchant)
	if err != nil {
		return nil, err
	}
	if sess.Endpoint != c.cfg.Endpoint {
		// A token issued for one resource server must never be presented to
		// another — not even when an operator repoints the endpoint setting.
		return nil, fmt.Errorf("remotemcp: %s was linked against %s, not the configured %s; re-link the account", c.cfg.Merchant, sess.Endpoint, c.cfg.Endpoint)
	}
	return sess, nil
}

func (c *Client) connect(ctx context.Context) (*mcp.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session, nil
	}
	sess, err := c.LoadSession()
	if err != nil {
		return nil, err
	}
	c.authFailed.Store(false)

	oc := &oauth2.Config{
		ClientID:     sess.ClientID,
		ClientSecret: sess.ClientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: sess.AuthURL, TokenURL: sess.TokenURL, AuthStyle: sess.AuthStyle},
		Scopes:       sess.Scopes,
	}
	// oauth2 keeps this context for every future refresh, so it must be a
	// background context carrying the HTTP client — not a per-call context
	// that is about to be cancelled.
	refreshCtx := context.WithValue(context.Background(), oauth2.HTTPClient, c.base)
	source := &persistingSource{
		src:        oc.TokenSource(refreshCtx, sess.Token),
		store:      c.cfg.Store,
		sess:       sess,
		lastAccess: sess.Token.AccessToken,
		failed:     &c.authFailed,
	}
	baseRT := c.base.Transport
	if baseRT == nil {
		baseRT = http.DefaultTransport
	}
	authed := &http.Client{
		Transport: &oauth2.Transport{Source: source, Base: &authWatch{base: baseRT, failed: &c.authFailed}},
		// Never follow a redirect with a bearer token attached: oauth2.Transport
		// re-adds Authorization on every hop, including hops to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             c.cfg.Endpoint,
		HTTPClient:           authed,
		DisableStandaloneSSE: true, // request/response only; Algebra doesn't consume server push
		MaxRetries:           -1,   // never silently re-drive a stream in the middle of a checkout
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "project-algebra", Version: "0.1.0"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if c.authFailed.Load() {
			return nil, fmt.Errorf("%w: %v", ErrSessionExpired, err)
		}
		return nil, fmt.Errorf("remotemcp: connecting to %s: %w", c.cfg.Merchant, err)
	}
	c.session = cs
	return cs, nil
}

// fail classifies a call error and drops a session that can't be reused.
func (c *Client) fail(err error) error {
	if c.authFailed.Load() {
		c.reset()
		return fmt.Errorf("%w: %v", ErrSessionExpired, err)
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		c.reset()
	}
	return err
}

func (c *Client) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		_ = c.session.Close()
	}
	c.session = nil
	c.tools = nil
}

// Close ends the MCP session, if one is open.
func (c *Client) Close() error {
	c.reset()
	return nil
}

// Tools lists every tool the live server exposes (all pages), cached for
// the life of the session.
func (c *Client) Tools(ctx context.Context) (map[string]*mcp.Tool, error) {
	c.mu.Lock()
	if c.tools != nil {
		tools := c.tools
		c.mu.Unlock()
		return tools, nil
	}
	c.mu.Unlock()

	cs, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	tools := map[string]*mcp.Tool{}
	params := &mcp.ListToolsParams{}
	for page := 0; page < 50; page++ {
		res, err := cs.ListTools(ctx, params)
		if err != nil {
			return nil, c.fail(err)
		}
		for _, t := range res.Tools {
			tools[t.Name] = t
		}
		if res.NextCursor == "" {
			break
		}
		params = &mcp.ListToolsParams{Cursor: res.NextCursor}
	}
	c.mu.Lock()
	c.tools = tools
	c.mu.Unlock()
	return tools, nil
}

// Call invokes tool with args and decodes its result into out (which may be
// nil). The structured result is preferred; servers that only return text
// are accepted when that text is a JSON document. Nothing is inferred from
// free text.
func (c *Client) Call(ctx context.Context, tool string, args any, out any) error {
	cs, err := c.connect(ctx)
	if err != nil {
		return err
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return c.fail(err)
	}
	if res.NeedsInput() {
		return fmt.Errorf("%w (tool %s)", ErrInputRequired, tool)
	}
	if res.IsError {
		return &ToolError{Tool: tool, Message: SafeText(firstText(res), 500)}
	}
	if out == nil {
		return nil
	}
	raw, err := resultJSON(res)
	if err != nil {
		return fmt.Errorf("remotemcp: %s returned no decodable JSON result: %w", tool, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("remotemcp: decoding %s result: %w", tool, err)
	}
	return nil
}

func resultJSON(res *mcp.CallToolResult) ([]byte, error) {
	if res.StructuredContent != nil {
		return json.Marshal(res.StructuredContent)
	}
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			text := strings.TrimSpace(tc.Text)
			if json.Valid([]byte(text)) {
				return []byte(text), nil
			}
		}
	}
	return nil, errors.New("no structured content and no JSON text content")
}

func firstText(res *mcp.CallToolResult) string {
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	return "no error message"
}

func truncate(s string, n int) string { return sanitize.Truncate(s, n) }

// SafeText prepares merchant-supplied text (tool error messages, tool names)
// for status lines and reasons an agent may read. See sanitize.Text: it is
// still data, never instructions.
func SafeText(s string, max int) string { return sanitize.Text(s, max) }

// ToolRequirement is one tool a connector's mapping depends on, with the
// input properties the connector sends it.
type ToolRequirement struct {
	Name       string
	Properties []string
}

// ContractError lists every way the live server's tools differ from the
// contract a connector was written against.
type ContractError struct {
	Problems []string
}

func (e *ContractError) Error() string {
	return "remotemcp: live tool contract does not match: " + strings.Join(e.Problems, "; ")
}

// CheckTools verifies the live tool list against reqs. This is how
// connectors refuse to guess: a mismatch disables their capabilities
// instead of sending calls shaped for a contract the server no longer has.
func CheckTools(tools map[string]*mcp.Tool, reqs []ToolRequirement) error {
	var problems []string
	for _, r := range reqs {
		t, ok := tools[r.Name]
		if !ok {
			problems = append(problems, "missing tool "+r.Name)
			continue
		}
		props, err := inputProperties(t)
		if err != nil {
			problems = append(problems, r.Name+": unreadable input schema")
			continue
		}
		for _, p := range r.Properties {
			if !props[p] {
				problems = append(problems, r.Name+": no input property "+p)
			}
		}
	}
	if len(problems) > 0 {
		return &ContractError{Problems: problems}
	}
	return nil
}

func inputProperties(t *mcp.Tool) (map[string]bool, error) {
	raw, err := json.Marshal(t.InputSchema)
	if err != nil {
		return nil, err
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(schema.Properties))
	for name := range schema.Properties {
		out[name] = true
	}
	return out, nil
}

// ToolNames returns the sorted tool names, for status output and manifests.
func ToolNames(tools map[string]*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// persistingSource saves every newly issued access token (e.g. after a
// refresh) so a restart doesn't replay a refresh token the server may have
// already rotated.
type persistingSource struct {
	src    oauth2.TokenSource
	store  SessionStore
	failed *atomic.Bool

	mu         sync.Mutex
	sess       *Session
	lastAccess string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.src.Token()
	if err != nil {
		p.failed.Store(true)
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.AccessToken != p.lastAccess {
		updated := *p.sess
		updated.Token = tok
		// Best effort: a failed save only means the next process start
		// refreshes again (or asks the user to re-link if rotation made the
		// stored refresh token stale).
		if err := p.store.Save(&updated); err == nil {
			p.sess = &updated
		}
		p.lastAccess = tok.AccessToken
	}
	return tok, nil
}

type authWatch struct {
	base   http.RoundTripper
	failed *atomic.Bool
}

func (a *authWatch) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := a.base.RoundTrip(r)
	if err == nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		a.failed.Store(true)
	}
	return resp, err
}

// ValidateEndpoint accepts only https URLs on public hosts, without embedded
// credentials. allowInsecureLoopback additionally admits loopback URLs, for
// tests against httptest servers.
func ValidateEndpoint(raw string, allowInsecureLoopback bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("remotemcp: invalid URL %q", raw)
	}
	if u.User != nil {
		return fmt.Errorf("remotemcp: URL %q must not embed credentials", raw)
	}
	host := strings.ToLower(u.Hostname())
	loopback := isLoopback(host)
	if allowInsecureLoopback && loopback && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("remotemcp: URL %q must use https", raw)
	}
	if loopback || isPrivateIP(host) {
		return fmt.Errorf("remotemcp: URL %q points at a loopback or private address", raw)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isPrivateIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified())
}
