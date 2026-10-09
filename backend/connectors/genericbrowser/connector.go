// Package genericbrowser is the architectural placeholder for the
// BrowserExecutor-backed fallback connector (mandate §13/§61, Phase 6):
// tightly constrained, domain-allowlisted, non-CAPTCHA-bypassing browser
// automation for merchants with no official API.
//
// It is intentionally NOT implemented in this build. The mandate is
// explicit that browser execution comes only "after merchant/API flows are
// stable" and must never be "a generic unrestricted browser agent with
// payment access." Shipping even a minimal version now — before the
// domain-allowlist, ephemeral-environment, and redaction machinery
// described in mandate §13 exist — would be exactly the kind of unsafe
// shortcut this whole project exists to avoid. This file exists so the
// Connector interface has a named slot to implement into later.
package genericbrowser

import (
	"context"

	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type Connector struct{}

func New() *Connector { return &Connector{} }

func (c *Connector) Name() string                { return "generic-browser" }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeReal }

func (c *Connector) Capabilities() merchant.Capabilities {
	return merchant.Capabilities{Search: false, Cart: false, Checkout: false, Coupons: false, OrderTracking: false}
}

func (c *Connector) Status() merchant.Status {
	return merchant.Status{
		Integration: merchant.IntegrationNotImplemented,
		Ready:       false,
		Detail:      "Placeholder for constrained, domain-allowlisted browser-assisted checkout (Phase 6). Not implemented in this build.",
	}
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
		Reason: "browser-assisted checkout (Phase 6) is not implemented in this build",
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
)
