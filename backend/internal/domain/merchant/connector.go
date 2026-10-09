// Package merchant defines the MerchantConnector abstraction every merchant
// integration implements, and the normalized types (Product, Cart, Offer)
// that make merchants comparable to each other. A connector must never
// claim a capability it doesn't have (mandate §9: "Never pretend an
// unsupported capability exists").
package merchant

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/quote"
)

// ProviderMode labels how real a connector's backing is. Never let a mock
// or sandbox connector's output be mistaken for a real merchant response.
type ProviderMode string

const (
	ProviderModeReal    ProviderMode = "real"
	ProviderModeSandbox ProviderMode = "sandbox"
	ProviderModeMock    ProviderMode = "mock"
)

// Capabilities is the honest, per-connector feature matrix (mandate §9/§12).
// A connector reports what it can actually do; callers must check this
// before calling the corresponding method rather than assuming support.
type Capabilities struct {
	Search        bool `json:"search"`
	Cart          bool `json:"cart"`
	Checkout      bool `json:"checkout"`
	Coupons       bool `json:"coupons"`
	OrderTracking bool `json:"order_tracking"`
}

// Product is a normalized listing from a merchant's catalog.
type Product struct {
	MerchantProductID string     `json:"merchant_product_id"`
	Merchant          string     `json:"merchant"`
	Brand             string     `json:"brand,omitempty"`
	Name              string     `json:"name"`
	Variant           string     `json:"variant,omitempty"`
	Size              string     `json:"size,omitempty"`
	Category          string     `json:"category,omitempty"`
	PriceMinorUnits   int64      `json:"price_minor_units"`
	Currency          string     `json:"currency"`
	Available         bool       `json:"available"`
	DeliveryETA       *time.Time `json:"delivery_eta,omitempty"`
	URL               string     `json:"url,omitempty"`
	// Confidence in [0,1]: how sure discovery/normalization is that this
	// listing matches the requested item. Below a caller-defined threshold,
	// the item must be returned as uncertain rather than silently matched
	// (mandate §16).
	Confidence float64 `json:"confidence"`
}

type CartItem struct {
	MerchantProductID string `json:"merchant_product_id"`
	Quantity          int    `json:"quantity"`
}

type Cart struct {
	ID       string     `json:"cart_id"`
	Merchant string     `json:"merchant"`
	Items    []CartItem `json:"items"`
}

type DeliveryOption struct {
	ID            string     `json:"id"`
	Label         string     `json:"label"`
	FeeMinorUnits int64      `json:"fee_minor_units"`
	ETA           *time.Time `json:"eta,omitempty"`
}

// AuthRequest/AuthResult model whatever handshake a connector needs
// (OAuth token, official API key, or — never — a stored password). What
// goes in here is connector-specific and intentionally opaque at this
// layer.
type AuthRequest struct {
	UserID string
	Params map[string]string
}

type AuthResult struct {
	Authenticated bool
	ExpiresAt     *time.Time
}

// ExecutionStatus is the outcome of ExecuteCheckout.
type ExecutionStatus string

const (
	ExecutionSucceeded                  ExecutionStatus = "SUCCEEDED"
	ExecutionAuthenticationRequired     ExecutionStatus = "AUTHENTICATION_REQUIRED"
	ExecutionMerchantInterventionNeeded ExecutionStatus = "MERCHANT_INTERVENTION_REQUIRED"
	ExecutionUserInterventionNeeded     ExecutionStatus = "USER_INTERVENTION_REQUIRED"
	ExecutionFailed                     ExecutionStatus = "FAILED"
)

// ShippingAddress / BillingAddress are the real, resolved values a merchant
// needs to ship and bill an order. They deliberately duplicate the shape of
// internal/domain/privacy's profiles rather than reusing those types: this
// is the ONLY struct shape allowed to cross into a merchant connector, and
// keeping it separate means the merchant layer never imports (and so can
// never reach into) the privacy storage domain.
type ShippingAddress struct {
	RecipientName string
	Line1         string
	Line2         string
	City          string
	State         string
	PostalCode    string
	Country       string
	Phone         string
}

type BillingAddress struct {
	Name       string
	Line1      string
	Line2      string
	City       string
	State      string
	PostalCode string
	Country    string
}

// Fulfillment carries privacy-resolved values into a checkout call. It is
// built ONLY inside OrderService.Execute, immediately before the connector
// call, from PrivacyResolver output — it does not exist earlier in the
// flow, is never persisted, and never crosses an agent-facing boundary
// (mandate §24/§27: the agent supplied only the alias "shipping:home"; the
// merchant gets the address; nothing in between ever holds both).
//
// Either field may be nil: a merchant that doesn't need a billing address
// (or an intent with no delivery profile) simply gets nothing.
type Fulfillment struct {
	Shipping *ShippingAddress
	Billing  *BillingAddress
}

// ExecutionResult is what ExecuteCheckout returns. Exactly one of Order or
// Challenge is set, matching Status. A connector that cannot safely
// continue (CAPTCHA, anti-bot challenge, expired merchant session) MUST
// return ExecutionUserInterventionNeeded rather than fabricating a result
// (mandate §10/§13).
type ExecutionResult struct {
	Status    ExecutionStatus
	Order     *order.Order
	Challenge *payment.AuthorizationChallenge
	Reason    string
}

// Connector is the interface every merchant integration implements. MCP,
// REST, and internal services all reach a merchant exclusively through
// this — there is no other path to a merchant's checkout.
type Connector interface {
	Name() string
	Mode() ProviderMode
	Capabilities() Capabilities

	Authenticate(ctx context.Context, req AuthRequest) (*AuthResult, error)

	SearchProducts(ctx context.Context, query string, limit int) ([]Product, error)
	GetProduct(ctx context.Context, merchantProductID string) (*Product, error)
	GetOffers(ctx context.Context, merchantProductID string) ([]quote.Offer, error)

	CreateCart(ctx context.Context, userRef string) (*Cart, error)
	AddToCart(ctx context.Context, cartID, merchantProductID string, qty int) (*Cart, error)
	RemoveFromCart(ctx context.Context, cartID, merchantProductID string) (*Cart, error)

	GetDeliveryOptions(ctx context.Context, cartID string, shippingAlias string) ([]DeliveryOption, error)
	ApplyCoupon(ctx context.Context, cartID, code string) (*Cart, error)
	GetCheckoutQuote(ctx context.Context, cartID string) (*quote.CheckoutQuote, error)

	// ExecuteCheckout performs the actual purchase. approvalID is passed
	// through so connectors that log/reconcile against Algebra's approval
	// record can do so; it is never sufficient authorization on its own —
	// the caller (internal/app/order_service) has already verified the
	// approval before calling this. fulfillment carries the
	// privacy-resolved shipping/billing values, materialized by the caller
	// only for this call — see Fulfillment.
	ExecuteCheckout(ctx context.Context, cartID string, approvalID string, fulfillment Fulfillment) (*ExecutionResult, error)

	GetOrder(ctx context.Context, merchantOrderID string) (*order.Order, error)
	CancelOrder(ctx context.Context, merchantOrderID string) error
}

// AllowedDomains gates outbound connector calls against SSRF and merchant
// URL manipulation (mandate §49). ValidateURL must be called before any
// connector follows a merchant-supplied URL (e.g. a redirect during
// checkout).
type AllowedDomains struct {
	domains map[string]bool
}

func NewAllowedDomains(domains ...string) *AllowedDomains {
	m := make(map[string]bool, len(domains))
	for _, d := range domains {
		m[strings.ToLower(d)] = true
	}
	return &AllowedDomains{domains: m}
}

// ValidateURL rejects anything that isn't https, has no host, resolves to a
// loopback/private/link-local address, or whose host (or a parent domain of
// it) isn't on the allowlist.
//
// The private-address check is deliberately independent of the allowlist:
// even a misconfigured allowlist (or one an operator widened carelessly)
// cannot turn this into an SSRF vector against 127.0.0.1, a private subnet,
// or a cloud metadata endpoint at 169.254.169.254 (mandate §47/§49).
func (a *AllowedDomains) ValidateURL(raw string) error {
	u, err := ValidatePublicHTTPSURL(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Hostname())
	for d := range a.domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return nil
		}
	}
	return &ErrDomainNotAllowed{URL: raw, Reason: "host not on allowlist"}
}

// ValidatePublicHTTPSURL applies the same https-only,
// no-loopback/private/link-local-address rule ValidateURL does, without a
// domain allowlist — for a caller that must accept any public domain (e.g.
// general web search results, not one merchant's own catalog) but still
// has to refuse an SSRF-shaped URL. Returns the parsed URL on success.
func ValidatePublicHTTPSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, &ErrDomainNotAllowed{URL: raw, Reason: "unparseable URL"}
	}
	if u.Scheme != "https" {
		return nil, &ErrDomainNotAllowed{URL: raw, Reason: "scheme must be https"}
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, &ErrDomainNotAllowed{URL: raw, Reason: "missing host"}
	}
	if isPrivateHost(host) {
		return nil, &ErrDomainNotAllowed{URL: raw, Reason: "host resolves to a loopback, private, or link-local address"}
	}
	return u, nil
}

// isPrivateHost reports whether a host is a literal IP in a
// loopback/private/link-local range, or the "localhost" name. It does not
// perform DNS resolution — a connector that follows a URL is responsible
// for its own egress restrictions at the network layer (mandate §49); this
// blocks the obvious, cheap cases at the domain boundary.
func isPrivateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// SanitizeProductURL returns raw if it passes ValidateURL, or "" if it does
// not. Used on URLs that came back FROM a merchant: a compromised or
// misbehaving connector must not be able to hand an agent (or, later, a
// browser executor) a link pointing at an internal address. Blanking the
// field rather than dropping the whole product keeps the price/availability
// information usable while refusing to propagate the unsafe link.
func (a *AllowedDomains) SanitizeProductURL(raw string) string {
	if raw == "" {
		return ""
	}
	if err := a.ValidateURL(raw); err != nil {
		return ""
	}
	return raw
}

type ErrDomainNotAllowed struct {
	URL    string
	Reason string
}

func (e *ErrDomainNotAllowed) Error() string {
	return "merchant: URL rejected (" + e.Reason + "): " + e.URL
}
