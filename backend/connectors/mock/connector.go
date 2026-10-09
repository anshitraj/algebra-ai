// Package mock implements a fully functional, deterministic MerchantConnector
// used for local development and the sandbox end-to-end test (mandate §54).
// It never talks to a network. Every price, product, and order it produces
// is clearly provider_mode=mock — this must never be confused with, or
// presented as, a real merchant response.
package mock

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

type catalogEntry struct {
	id       string
	brand    string
	name     string
	category string
	price    int64 // minor units, INR
}

// catalog is a small, fixed product list covering the mandate's own example
// prompts ("Coke Zero and chips", "ingredients to cook pasta for four") plus
// a few clothing entries so a CommerceProfile-driven "buy me a jacket"
// scenario (internal/domain/commerceprofile) has something real to find —
// color is deliberately the first word of each name so it disambiguates
// cleanly through findProducts' first-word match below, and casual/formal
// items never share a color so a color-only query can't cross-match them.
var catalog = []catalogEntry{
	{id: "mock-coke-zero-750ml", brand: "Coca-Cola", name: "Coke Zero 750ml", category: "beverages", price: 6000},
	{id: "mock-lays-chips-52g", brand: "Lay's", name: "Classic Salted Chips 52g", category: "snacks", price: 2000},
	{id: "mock-pasta-500g", brand: "Barilla", name: "Penne Pasta 500g", category: "groceries", price: 12000},
	{id: "mock-tomato-sauce-400g", brand: "Del Monte", name: "Tomato Pasta Sauce 400g", category: "groceries", price: 15000},
	{id: "mock-garlic-bread-250g", brand: "Local Bakery", name: "Garlic Bread 250g", category: "groceries", price: 8000},
	{id: "mock-parmesan-100g", brand: "Go Cheese", name: "Parmesan Cheese 100g", category: "groceries", price: 22000},
	{id: "mock-gift-card-500", brand: "Generic", name: "₹500 Gift Card", category: "gift_cards", price: 50000},
	{id: "mock-jacket-black-casual-l", brand: "Urban Basics", name: "Black Casual Jacket - L", category: "clothing", price: 350000},
	{id: "mock-jacket-navy-casual-l", brand: "Urban Basics", name: "Navy Casual Jacket - L", category: "clothing", price: 380000},
	{id: "mock-blazer-grey-formal-l", brand: "Formal Co", name: "Grey Formal Blazer - L", category: "clothing", price: 550000},
}

func findProducts(query string, limit int) []merchant.Product {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []merchant.Product
	for _, c := range catalog {
		name := strings.ToLower(c.name)
		if q == "" || strings.Contains(name, q) || strings.Contains(q, strings.Split(name, " ")[0]) {
			out = append(out, merchant.Product{
				MerchantProductID: c.id,
				Merchant:          "mock",
				Brand:             c.brand,
				Name:              c.name,
				Category:          c.category,
				PriceMinorUnits:   c.price,
				Currency:          "INR",
				Available:         true,
				Confidence:        0.92,
			})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func findByID(id string) *catalogEntry {
	for i := range catalog {
		if catalog[i].id == id {
			return &catalog[i]
		}
	}
	return nil
}

type cartState struct {
	items  []merchant.CartItem
	coupon string
}

// Connector is the mock MerchantConnector. Safe for concurrent use.
type Connector struct {
	mu              sync.Mutex
	carts           map[string]*cartState
	orders          map[string]*order.Order
	lastFulfillment merchant.Fulfillment
	now             func() time.Time
}

func New() *Connector {
	return &Connector{
		carts:  map[string]*cartState{},
		orders: map[string]*order.Order{},
		now:    time.Now,
	}
}

func (c *Connector) Name() string                { return "mock" }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeMock }

func (c *Connector) Capabilities() merchant.Capabilities {
	return merchant.Capabilities{Search: true, Cart: true, Checkout: true, Coupons: true, OrderTracking: true}
}

func (c *Connector) Status() merchant.Status {
	return merchant.Status{
		Integration: merchant.IntegrationMock,
		Ready:       true,
		Detail:      "Deterministic in-memory catalog for development and tests. Nothing it returns is a real merchant response.",
	}
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	return &merchant.AuthResult{Authenticated: true}, nil
}

func (c *Connector) SearchProducts(_ context.Context, query string, limit int) ([]merchant.Product, error) {
	if limit <= 0 {
		limit = 10
	}
	return findProducts(query, limit), nil
}

func (c *Connector) GetProduct(_ context.Context, merchantProductID string) (*merchant.Product, error) {
	entry := findByID(merchantProductID)
	if entry == nil {
		return nil, fmt.Errorf("%w: mock product %s", shared.ErrNotFound, merchantProductID)
	}
	return &merchant.Product{
		MerchantProductID: entry.id, Merchant: "mock", Brand: entry.brand, Name: entry.name,
		Category: entry.category, PriceMinorUnits: entry.price, Currency: "INR", Available: true, Confidence: 1,
	}, nil
}

func (c *Connector) GetOffers(_ context.Context, merchantProductID string) ([]quote.Offer, error) {
	return []quote.Offer{
		{Type: quote.OfferConditional, Description: "10% off with code SAVE10", Code: "SAVE10"},
	}, nil
}

func (c *Connector) CreateCart(_ context.Context, _ string) (*merchant.Cart, error) {
	id := "mockcart_" + uuid.NewString()
	c.mu.Lock()
	c.carts[id] = &cartState{}
	c.mu.Unlock()
	return &merchant.Cart{ID: id, Merchant: "mock"}, nil
}

func (c *Connector) getCart(cartID string) (*cartState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cart, ok := c.carts[cartID]
	if !ok {
		return nil, fmt.Errorf("%w: mock cart %s", shared.ErrNotFound, cartID)
	}
	return cart, nil
}

func (c *Connector) AddToCart(_ context.Context, cartID, merchantProductID string, qty int) (*merchant.Cart, error) {
	if findByID(merchantProductID) == nil {
		return nil, fmt.Errorf("%w: mock product %s", shared.ErrNotFound, merchantProductID)
	}
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, it := range cart.items {
		if it.MerchantProductID == merchantProductID {
			cart.items[i].Quantity += qty
			return c.cartView(cartID, cart), nil
		}
	}
	cart.items = append(cart.items, merchant.CartItem{MerchantProductID: merchantProductID, Quantity: qty})
	return c.cartView(cartID, cart), nil
}

func (c *Connector) RemoveFromCart(_ context.Context, cartID, merchantProductID string) (*merchant.Cart, error) {
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	filtered := cart.items[:0]
	for _, it := range cart.items {
		if it.MerchantProductID != merchantProductID {
			filtered = append(filtered, it)
		}
	}
	cart.items = filtered
	return c.cartView(cartID, cart), nil
}

func (c *Connector) cartView(cartID string, cart *cartState) *merchant.Cart {
	items := make([]merchant.CartItem, len(cart.items))
	copy(items, cart.items)
	return &merchant.Cart{ID: cartID, Merchant: "mock", Items: items}
}

func (c *Connector) GetDeliveryOptions(_ context.Context, cartID string, _ string) ([]merchant.DeliveryOption, error) {
	if _, err := c.getCart(cartID); err != nil {
		return nil, err
	}
	eta := c.now().Add(2 * time.Hour)
	return []merchant.DeliveryOption{{ID: "standard", Label: "Standard delivery", FeeMinorUnits: 2000, ETA: &eta}}, nil
}

func (c *Connector) ApplyCoupon(_ context.Context, cartID, code string) (*merchant.Cart, error) {
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	if strings.ToUpper(code) != "SAVE10" {
		return nil, fmt.Errorf("mock: coupon %q is invalid or expired", code)
	}
	c.mu.Lock()
	cart.coupon = "SAVE10"
	c.mu.Unlock()
	return c.cartView(cartID, cart), nil
}

func (c *Connector) GetCheckoutQuote(_ context.Context, cartID string) (*quote.CheckoutQuote, error) {
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	items := make([]merchant.CartItem, len(cart.items))
	copy(items, cart.items)
	coupon := cart.coupon
	c.mu.Unlock()

	if len(items) == 0 {
		return nil, fmt.Errorf("mock: cart %s is empty", cartID)
	}

	var subtotal int64
	quoteItems := make([]quote.Item, 0, len(items))
	for _, it := range items {
		entry := findByID(it.MerchantProductID)
		if entry == nil {
			continue
		}
		lineTotal := entry.price * int64(it.Quantity)
		subtotal += lineTotal
		quoteItems = append(quoteItems, quote.Item{
			MerchantProductID: entry.id, Name: entry.name, Quantity: it.Quantity,
			UnitPrice: money.Amount{MinorUnits: entry.price, Currency: "INR"},
		})
	}

	var couponDiscount int64
	if coupon == "SAVE10" {
		couponDiscount = subtotal / 10
	}

	now := c.now()
	q := &quote.CheckoutQuote{
		QuoteID:        "mockquote_" + uuid.NewString(),
		CartID:         cartID,
		Merchant:       "mock",
		Items:          quoteItems,
		Subtotal:       money.Amount{MinorUnits: subtotal, Currency: "INR"},
		CouponDiscount: money.Amount{MinorUnits: couponDiscount, Currency: "INR"},
		DeliveryFee:    money.Amount{MinorUnits: 2000, Currency: "INR"},
		PlatformFee:    money.Amount{MinorUnits: 500, Currency: "INR"},
		RetrievedAt:    now,
		ExpiresAt:      now.Add(5 * time.Minute),
	}
	q.Recompute()
	return q, nil
}

// LastFulfillment returns the privacy-resolved shipping/billing values the
// most recent ExecuteCheckout received. Tests use it to assert the real
// address actually reached the merchant boundary (and, separately, that it
// never appears in any agent-facing response) — a real connector would
// instead put these on the wire to the merchant's own checkout API.
func (c *Connector) LastFulfillment() merchant.Fulfillment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastFulfillment
}

func (c *Connector) ExecuteCheckout(ctx context.Context, cartID string, approvalID string, fulfillment merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	q, err := c.GetCheckoutQuote(ctx, cartID)
	if err != nil {
		return nil, err
	}
	if fulfillment.Shipping == nil {
		// A real merchant would reject an undeliverable order outright
		// rather than guess an address — the mock does the same so the
		// "privacy resolution actually happened" path is exercised, not
		// silently skipped.
		return &merchant.ExecutionResult{
			Status: merchant.ExecutionUserInterventionNeeded,
			Reason: "mock: no shipping address was resolved for this order",
		}, nil
	}
	c.mu.Lock()
	c.lastFulfillment = fulfillment
	c.mu.Unlock()
	items := make([]order.Item, len(q.Items))
	for i, it := range q.Items {
		items[i] = order.Item{MerchantProductID: it.MerchantProductID, Name: it.Name, Quantity: it.Quantity, UnitPrice: it.UnitPrice}
	}
	ord := &order.Order{
		Merchant:        "mock",
		MerchantOrderID: "mockorder_" + uuid.NewString(),
		Items:           items,
		Total:           q.FinalPayable,
		Status:          order.StatusPlaced,
		PlacedAt:        c.now(),
	}
	c.mu.Lock()
	c.orders[ord.MerchantOrderID] = ord
	delete(c.carts, cartID)
	c.mu.Unlock()

	return &merchant.ExecutionResult{Status: merchant.ExecutionSucceeded, Order: ord}, nil
}

func (c *Connector) GetOrder(_ context.Context, merchantOrderID string) (*order.Order, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ord, ok := c.orders[merchantOrderID]
	if !ok {
		return nil, fmt.Errorf("%w: mock order %s", shared.ErrNotFound, merchantOrderID)
	}
	copyOrd := *ord
	return &copyOrd, nil
}

func (c *Connector) CancelOrder(_ context.Context, merchantOrderID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ord, ok := c.orders[merchantOrderID]
	if !ok {
		return fmt.Errorf("%w: mock order %s", shared.ErrNotFound, merchantOrderID)
	}
	ord.Status = order.StatusCancelled
	return nil
}

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
)
