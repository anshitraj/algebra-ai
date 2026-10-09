// Package blinkit offers Blinkit as a merchant option without automating it.
//
// Blinkit publishes no public API, partner catalog API, affiliate API, or
// MCP server that a third party can integrate against. The community
// "Blinkit MCP" projects that do exist work by driving blinkit.com's consumer
// web app with a headless browser and replaying its private endpoints, which
// means working around its Cloudflare/anti-bot protection. Mandate §10
// forbids exactly that ("Do NOT bypass: CAPTCHA, Cloudflare, rate limits,
// access controls, authentication protections, anti-bot systems"), so none
// of them are used or ported here.
//
// What this connector honestly does instead: it hands the USER a link to
// Blinkit's own search page for what they asked for (HandoffURL), so an agent
// can say "Blinkit can't be automated — here is the Blinkit search, finish
// there yourself". Every capability is false and ExecuteCheckout returns
// USER_INTERVENTION_REQUIRED. When Blinkit ships an official API or MCP, the
// real integration replaces this file.
package blinkit

import (
	"context"
	"net/url"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

const (
	homeURL   = "https://blinkit.com/"
	searchURL = "https://blinkit.com/s/"
)

type Connector struct{}

func New() *Connector { return &Connector{} }

func (c *Connector) Name() string                { return "blinkit" }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeReal }

// Capabilities is all-false by construction: nothing here can search, fill a
// cart, or check out on anyone's behalf.
func (c *Connector) Capabilities() merchant.Capabilities { return merchant.Capabilities{} }

func (c *Connector) Status() merchant.Status {
	return merchant.Status{
		Integration: merchant.IntegrationDeepLinkHandoff,
		Ready:       true,
		Detail:      "Blinkit publishes no official API or MCP server. Algebra only hands the user a link to Blinkit's own search page; search, cart and checkout happen in Blinkit's app or site, done by the user.",
		Source:      homeURL,
	}
}

// HandoffURL returns Blinkit's public search page for query. It is a link
// for a human to open — Algebra never fetches it.
func (c *Connector) HandoffURL(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return homeURL
	}
	return searchURL + "?q=" + url.QueryEscape(q)
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) SearchProducts(context.Context, string, int) ([]merchant.Product, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) GetProduct(context.Context, string) (*merchant.Product, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) GetOffers(context.Context, string) ([]quote.Offer, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) CreateCart(context.Context, string) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) AddToCart(context.Context, string, string, int) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) RemoveFromCart(context.Context, string, string) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) GetDeliveryOptions(context.Context, string, string) ([]merchant.DeliveryOption, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) ApplyCoupon(context.Context, string, string) (*merchant.Cart, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) GetCheckoutQuote(context.Context, string) (*quote.CheckoutQuote, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) ExecuteCheckout(context.Context, string, string, merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	return &merchant.ExecutionResult{
		Status: merchant.ExecutionUserInterventionNeeded,
		Reason: "Blinkit has no official API or MCP server; the user must place this order in the Blinkit app or site themselves",
	}, nil
}
func (c *Connector) GetOrder(context.Context, string) (*order.Order, error) {
	return nil, shared.ErrNotImplemented
}
func (c *Connector) CancelOrder(context.Context, string) error {
	return shared.ErrNotImplemented
}

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
	_ merchant.HandoffLinker  = (*Connector)(nil)
)
