// Package zepto connects to Zepto's official hosted MCP server,
// https://mcp.zepto.co.in/mcp (github.com/zeptonow/mcp).
//
// Verified facts this connector is built on:
//
//   - The server is OAuth-protected. An unauthenticated request gets a 401
//     whose challenge points at
//     https://mcp.zepto.co.in/.well-known/oauth-protected-resource, which
//     names https://auth.zepto.co.in as the authorization server with scopes
//     tools:read, tools:write and dev.ucp.shopping.cart:manage. That server
//     supports dynamic client registration, PKCE S256 and public clients,
//     and Zepto allowlists loopback redirects — so cmd/merchant-login links
//     a user's Zepto account the standard way, with the user entering their
//     phone number and OTP on Zepto's own page.
//   - Zepto documents WHAT the server does (live catalog search, cart, real
//     order placement with COD/UPI/cards/Zepto Cash, order history) but
//     publishes no tool names or argument/result schemas.
//
// So this connector links, connects and lists the live tools — and stops
// there. Mapping search/cart/checkout onto tool shapes nobody has published
// would mean guessing a wire format for REAL orders (mandate §10/§75), so
// every capability stays false. cmd/merchant-login writes the live tool
// manifest (names and JSON schemas, no user data) to
// .data/merchant-tools/zepto.json; once those schemas are reviewed, the
// mapping belongs here, guarded by remotemcp.CheckTools exactly like
// connectors/swiggyinstamart.
//
// Until then Zepto is still an honest option: agents get a link to Zepto's
// own search page (HandoffURL), and ExecuteCheckout hands the order back to
// the user.
package zepto

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/connectors/remotemcp"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

const (
	Name            = "zepto"
	DefaultEndpoint = "https://mcp.zepto.co.in/mcp"
	DocsURL         = "https://github.com/zeptonow/mcp"

	homeURL     = "https://www.zeptonow.com/"
	searchURL   = "https://www.zeptonow.com/search"
	rewarmAfter = time.Minute
)

type Connector struct {
	mcp *remotemcp.Client
	now func() time.Time

	mu        sync.Mutex
	detail    string
	checkedAt time.Time
	checked   bool
	warming   bool
}

// New builds the connector. client may be nil when the endpoint is not
// configured; the connector then reports exactly that.
func New(client *remotemcp.Client) *Connector {
	return &Connector{mcp: client, now: time.Now}
}

func (c *Connector) Name() string                { return Name }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeReal }

// Capabilities stays all-false until Zepto's tool contract is published or
// verified and mapped — see the package doc.
func (c *Connector) Capabilities() merchant.Capabilities {
	c.maybeRewarm()
	return merchant.Capabilities{}
}

func (c *Connector) Status() merchant.Status {
	c.maybeRewarm()
	c.mu.Lock()
	defer c.mu.Unlock()
	detail := c.detail
	if detail == "" {
		detail = "Checking the Zepto MCP connection."
	}
	return merchant.Status{Integration: merchant.IntegrationOfficialMCP, Ready: false, Detail: detail, Source: DocsURL}
}

func (c *Connector) maybeRewarm() {
	c.mu.Lock()
	stale := !c.warming && (!c.checked || c.now().Sub(c.checkedAt) > rewarmAfter)
	if stale {
		c.warming = true
	}
	c.mu.Unlock()
	if stale {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = c.Warm(ctx)
		}()
	}
}

func (c *Connector) setDetail(detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.detail, c.checkedAt, c.checked, c.warming = detail, c.now(), true, false
}

// Warm checks whether an account is linked and, if so, lists the live tools
// so the status can say exactly what the server exposes.
func (c *Connector) Warm(ctx context.Context) error {
	if c.mcp == nil {
		c.setDetail("Zepto MCP endpoint is not configured.")
		return nil
	}
	_, err := c.mcp.LoadSession()
	switch {
	case errors.Is(err, remotemcp.ErrNotLinked):
		c.setDetail("No Zepto account linked. Run: go run ./cmd/merchant-login -merchant zepto (you sign in with your phone number and OTP on Zepto's own page). Search, cart and checkout stay off even after linking until Zepto's tool schemas are verified; use the handoff link meanwhile.")
		return nil
	case err != nil:
		c.setDetail(remotemcp.SafeText(err.Error(), 300))
		return err
	}
	tools, err := c.mcp.Tools(ctx)
	if err != nil {
		c.setDetail("Zepto account linked, but its MCP server could not be listed: " + remotemcp.SafeText(err.Error(), 300))
		return err
	}
	names := safeToolNames(remotemcp.ToolNames(tools), 20)
	c.setDetail(fmt.Sprintf("Zepto account linked; the live server exposes %d tools (%s). Zepto publishes no argument/result schemas for them, so Algebra does not drive search, cart or checkout through them yet. Review .data/merchant-tools/zepto.json (written by merchant-login) and map verified tools in connectors/zepto; use the handoff link meanwhile.", len(tools), strings.Join(names, ", ")))
	return nil
}

var toolNameRE = regexp.MustCompile(`^[A-Za-z0-9_.\-/]{1,64}$`)

// safeToolNames keeps only well-formed tool names: tool names come from the
// remote server and end up in text an agent reads.
func safeToolNames(names []string, max int) []string {
	var out []string
	for _, n := range names {
		if toolNameRE.MatchString(n) {
			out = append(out, n)
		}
		if len(out) == max {
			break
		}
	}
	return out
}

// HandoffURL returns Zepto's public web search page for query — a link for
// a human to open; Algebra never fetches it.
func (c *Connector) HandoffURL(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return homeURL
	}
	return searchURL + "?query=" + url.QueryEscape(q)
}

func (c *Connector) notMapped() error {
	return fmt.Errorf("%w: Zepto's MCP tool schemas are unpublished and not yet mapped (see connectors/zepto)", shared.ErrNotImplemented)
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	if c.mcp == nil {
		return &merchant.AuthResult{Authenticated: false}, nil
	}
	sess, err := c.mcp.LoadSession()
	if err != nil {
		return &merchant.AuthResult{Authenticated: false}, nil
	}
	res := &merchant.AuthResult{Authenticated: true}
	if !sess.Token.Expiry.IsZero() {
		exp := sess.Token.Expiry
		res.ExpiresAt = &exp
	}
	return res, nil
}

func (c *Connector) SearchProducts(context.Context, string, int) ([]merchant.Product, error) {
	return nil, c.notMapped()
}
func (c *Connector) GetProduct(context.Context, string) (*merchant.Product, error) {
	return nil, c.notMapped()
}
func (c *Connector) GetOffers(context.Context, string) ([]quote.Offer, error) {
	return nil, c.notMapped()
}
func (c *Connector) CreateCart(context.Context, string) (*merchant.Cart, error) {
	return nil, c.notMapped()
}
func (c *Connector) AddToCart(context.Context, string, string, int) (*merchant.Cart, error) {
	return nil, c.notMapped()
}
func (c *Connector) RemoveFromCart(context.Context, string, string) (*merchant.Cart, error) {
	return nil, c.notMapped()
}
func (c *Connector) GetDeliveryOptions(context.Context, string, string) ([]merchant.DeliveryOption, error) {
	return nil, c.notMapped()
}
func (c *Connector) ApplyCoupon(context.Context, string, string) (*merchant.Cart, error) {
	return nil, c.notMapped()
}
func (c *Connector) GetCheckoutQuote(context.Context, string) (*quote.CheckoutQuote, error) {
	return nil, c.notMapped()
}

func (c *Connector) ExecuteCheckout(context.Context, string, string, merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	return &merchant.ExecutionResult{
		Status: merchant.ExecutionUserInterventionNeeded,
		Reason: "Zepto's MCP tool schemas are unpublished and not yet mapped, so Algebra cannot place Zepto orders; the user can finish in the Zepto app",
	}, nil
}

func (c *Connector) GetOrder(context.Context, string) (*order.Order, error) {
	return nil, c.notMapped()
}
func (c *Connector) CancelOrder(context.Context, string) error {
	return c.notMapped()
}

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
	_ merchant.HandoffLinker  = (*Connector)(nil)
	_ merchant.Warmer         = (*Connector)(nil)
)
