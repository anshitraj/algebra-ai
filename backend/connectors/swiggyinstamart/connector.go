// Package swiggyinstamart connects Swiggy Instamart through Swiggy's
// official hosted MCP server, https://mcp.swiggy.com/im, built against the
// published tool reference at
// https://mcp.swiggy.com/builders/docs/reference/instamart/.
//
// What is real here, and what bounds it:
//
//   - Account linking is the user's own Swiggy login — phone number and OTP
//     typed into Swiggy's consent page in their own browser — via
//     `go run ./cmd/merchant-login -merchant swiggy_instamart`. At link time
//     the user also picks which saved Swiggy address Algebra's shipping
//     alias (e.g. "shipping:home") stands for. Swiggy places orders against
//     saved address IDs, so the street address Algebra's privacy layer
//     resolves is used only as a PIN-code cross-check and is never sent.
//   - The tool contract is checked against the live tools/list (Warm). Any
//     mismatch turns every capability off rather than guessing.
//   - Orders are REAL and placed Cash on Delivery only. Algebra is
//     non-custodial and never pays Swiggy on anyone's behalf; the UPI flow
//     (the user approves an intent in their UPI app, then the order is
//     confirmed) is not wired in this build.
//   - Swiggy's builder program lets anyone build against localhost, but
//     production access is reviewed (builders@swiggy.in). That is why this
//     connector is opt-in via ENABLED_MERCHANTS.
//   - One linked account has one server-side cart, and update_cart replaces
//     all of it. Every quote and checkout re-reads that cart and refuses to
//     continue unless it is exactly what Algebra put there.
//   - Swiggy returns item prices as JSON numbers without stating the unit;
//     they are read as rupees. Search prices are informational only — the
//     quote's FinalPayable always equals get_cart's own "To Pay" value.
package swiggyinstamart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/project-algebra/algebra/connectors/remotemcp"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

const (
	Name            = "swiggy_instamart"
	DefaultEndpoint = "https://mcp.swiggy.com/im"
	DocsURL         = "https://mcp.swiggy.com/builders/docs/reference/instamart/"

	// SettingAddressID and SettingShippingAlias are written by
	// cmd/merchant-login into the linked session.
	SettingAddressID     = "address_id"
	SettingShippingAlias = "shipping_alias"

	// PaymentRequirementCOD is set on every quote so policy and the approval
	// screen show how the order will actually be paid.
	PaymentRequirementCOD = "CASH_ON_DELIVERY"

	quoteTTL    = 2 * time.Minute
	cartMaxAge  = time.Hour
	rewarmAfter = time.Minute
)

// Contract is the part of the published Instamart reference this connector
// calls, with the argument names it sends.
var Contract = []remotemcp.ToolRequirement{
	{Name: "get_addresses", Properties: []string{"page"}},
	{Name: "search_products", Properties: []string{"addressId", "query"}},
	{Name: "update_cart", Properties: []string{"selectedAddressId", "items"}},
	{Name: "clear_cart"},
	{Name: "get_cart"},
	{Name: "get_payment_options"},
	{Name: "checkout", Properties: []string{"addressId", "paymentMethod"}},
	{Name: "get_orders", Properties: []string{"count"}},
}

type readiness int

const (
	stateUnchecked readiness = iota
	stateNotLinked
	stateNeedsSetup
	stateUnavailable
	stateReady
)

type Connector struct {
	mcp *remotemcp.Client
	now func() time.Time

	mu        sync.Mutex
	state     readiness
	detail    string
	checkedAt time.Time
	warming   bool
	carts     map[string]*localCart

	// remoteMu serializes everything that reads or replaces the account's
	// single server-side cart, and the checkout itself.
	remoteMu sync.Mutex
}

type localCart struct {
	id            string
	addressID     string
	aliasVerified bool
	createdAt     time.Time
	lines         []cartLine
}

type cartLine struct {
	spinID, skuID string
	qty           int
}

func New(client *remotemcp.Client) *Connector {
	return &Connector{mcp: client, now: time.Now, carts: map[string]*localCart{}}
}

func (c *Connector) Name() string                { return Name }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeReal }

func (c *Connector) Capabilities() merchant.Capabilities {
	if !c.ready() {
		return merchant.Capabilities{}
	}
	return merchant.Capabilities{Search: true, Cart: true, Checkout: true, OrderTracking: true}
}

func (c *Connector) Status() merchant.Status {
	ready := c.ready()
	c.mu.Lock()
	defer c.mu.Unlock()
	detail := c.detail
	if detail == "" {
		detail = "Checking the Swiggy Instamart connection."
	}
	return merchant.Status{Integration: merchant.IntegrationOfficialMCP, Ready: ready, Detail: detail, Source: DocsURL}
}

// ready reports the last known readiness, and re-checks in the background
// when a not-ready state is stale — so linking an account (or Swiggy
// recovering) is picked up without a restart.
func (c *Connector) ready() bool {
	c.mu.Lock()
	state := c.state
	stale := state != stateReady && !c.warming && c.now().Sub(c.checkedAt) > rewarmAfter
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
	return state == stateReady
}

func (c *Connector) setState(s readiness, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state, c.detail, c.checkedAt, c.warming = s, detail, c.now(), false
}

// Warm validates the linked session and the live tool contract.
func (c *Connector) Warm(ctx context.Context) error {
	sess, err := c.mcp.LoadSession()
	switch {
	case errors.Is(err, remotemcp.ErrNotLinked):
		c.setState(stateNotLinked, "No Swiggy account linked. Run: go run ./cmd/merchant-login -merchant swiggy_instamart -alias shipping:home (you sign in with your phone number and OTP on Swiggy's own page).")
		return nil
	case err != nil:
		c.setState(stateUnavailable, remotemcp.SafeText(err.Error(), 300))
		return err
	}
	if sess.Settings[SettingAddressID] == "" || sess.Settings[SettingShippingAlias] == "" {
		c.setState(stateNeedsSetup, "Swiggy account linked, but no delivery address was chosen for a shipping alias. Re-run merchant-login.")
		return nil
	}
	tools, err := c.mcp.Tools(ctx)
	if err != nil {
		c.setState(stateUnavailable, "Could not list Swiggy Instamart MCP tools: "+remotemcp.SafeText(err.Error(), 300))
		return err
	}
	if err := remotemcp.CheckTools(tools, Contract); err != nil {
		c.setState(stateUnavailable, "Swiggy's live tools no longer match the published Instamart reference, so every capability is off: "+remotemcp.SafeText(err.Error(), 400))
		return err
	}
	c.setState(stateReady, "Linked Swiggy account; live tools match the published Instamart reference. Orders are REAL and placed Cash on Delivery.")
	return nil
}

func (c *Connector) settings() (addressID, alias string, err error) {
	sess, err := c.mcp.LoadSession()
	if err != nil {
		return "", "", err
	}
	addressID, alias = sess.Settings[SettingAddressID], sess.Settings[SettingShippingAlias]
	if addressID == "" {
		return "", "", errors.New("swiggy_instamart: no delivery address chosen; re-run merchant-login")
	}
	return addressID, alias, nil
}

// call invokes a Swiggy tool and unwraps Swiggy's uniform
// {"success", "data", "message" | "error"} envelope.
func call[T any](ctx context.Context, c *Connector, tool string, args any) (T, error) {
	var zero T
	var env struct {
		Success bool   `json:"success"`
		Data    T      `json:"data"`
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.mcp.Call(ctx, tool, args, &env); err != nil {
		if errors.Is(err, remotemcp.ErrSessionExpired) || errors.Is(err, remotemcp.ErrNotLinked) {
			c.setState(stateNotLinked, "The Swiggy session is no longer valid. Re-run merchant-login.")
		}
		return zero, err
	}
	if !env.Success {
		msg := env.Message
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Message
		}
		if msg == "" {
			msg = "no error message"
		}
		return zero, &remotemcp.ToolError{Tool: tool, Message: remotemcp.SafeText(msg, 300)}
	}
	return env.Data, nil
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
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

// flexString accepts a JSON string or number.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(n.String())
	return nil
}

type slaInfo struct {
	Value flexString `json:"value"`
	Unit  string     `json:"unit"`
}

type searchData struct {
	Products []struct {
		DisplayName string `json:"displayName"`
		Brand       string `json:"brand"`
		IsPromoted  bool   `json:"isPromoted"`
		Variations  []struct {
			SpinID              string `json:"spinId"`
			SkuID               string `json:"skuId"`
			QuantityDescription string `json:"quantityDescription"`
			DisplayName         string `json:"displayName"`
			BrandName           string `json:"brandName"`
			Price               struct {
				OfferPrice float64 `json:"offerPrice"`
			} `json:"price"`
			IsInStockAndAvailable bool     `json:"isInStockAndAvailable"`
			SLA                   *slaInfo `json:"sla"`
		} `json:"variations"`
	} `json:"products"`
}

func (c *Connector) SearchProducts(ctx context.Context, query string, limit int) ([]merchant.Product, error) {
	if limit <= 0 {
		limit = 10
	}
	addressID, _, err := c.settings()
	if err != nil {
		return nil, err
	}
	data, err := call[searchData](ctx, c, "search_products", map[string]any{"addressId": addressID, "query": query})
	if err != nil {
		return nil, err
	}
	now := c.now()
	var out []merchant.Product
	rank := 0
	for _, p := range data.Products {
		for _, v := range p.Variations {
			if len(out) >= limit {
				return out, nil
			}
			price, ok := rupeesToPaise(v.Price.OfferPrice)
			if v.SpinID == "" || !ok {
				continue
			}
			name := v.DisplayName
			if name == "" {
				name = p.DisplayName
			}
			brand := v.BrandName
			if brand == "" {
				brand = p.Brand
			}
			out = append(out, merchant.Product{
				MerchantProductID: encodeProductID(v.SpinID, v.SkuID),
				Merchant:          Name,
				Brand:             remotemcp.SafeText(brand, 80),
				Name:              remotemcp.SafeText(name, 160),
				Size:              remotemcp.SafeText(v.QuantityDescription, 60),
				PriceMinorUnits:   price,
				Currency:          "INR",
				Available:         v.IsInStockAndAvailable,
				DeliveryETA:       etaFromSLA(now, v.SLA),
				Confidence:        rankConfidence(rank, p.IsPromoted),
			})
			rank++
		}
	}
	return out, nil
}

// rankConfidence turns Swiggy's own result order into Product.Confidence.
// It reflects the merchant's search ranking, not an independent check that
// the product is what the user asked for; promoted placements are
// discounted because an ad slot says nothing about relevance.
func rankConfidence(rank int, promoted bool) float64 {
	conf := 0.8 - 0.05*float64(rank)
	if promoted {
		conf -= 0.2
	}
	return math.Max(conf, 0.3)
}

func etaFromSLA(now time.Time, s *slaInfo) *time.Time {
	if s == nil || !strings.Contains(strings.ToLower(s.Unit), "min") {
		return nil
	}
	mins, err := strconv.Atoi(strings.TrimSpace(string(s.Value)))
	if err != nil || mins <= 0 || mins > 24*60 {
		return nil
	}
	eta := now.Add(time.Duration(mins) * time.Minute)
	return &eta
}

func (c *Connector) GetProduct(context.Context, string) (*merchant.Product, error) {
	return nil, fmt.Errorf("%w: Swiggy's Instamart MCP has no product-by-ID tool", shared.ErrNotImplemented)
}

func (c *Connector) GetOffers(context.Context, string) ([]quote.Offer, error) {
	return nil, fmt.Errorf("%w: Swiggy's Instamart MCP exposes no coupon tool", shared.ErrNotImplemented)
}

func (c *Connector) ApplyCoupon(context.Context, string, string) (*merchant.Cart, error) {
	return nil, fmt.Errorf("%w: Swiggy's Instamart MCP exposes no coupon tool", shared.ErrNotImplemented)
}

func (c *Connector) CreateCart(_ context.Context, _ string) (*merchant.Cart, error) {
	addressID, _, err := c.settings()
	if err != nil {
		return nil, err
	}
	now := c.now()
	cart := &localCart{id: "swiggyim_cart_" + uuid.NewString(), addressID: addressID, createdAt: now}
	c.mu.Lock()
	for id, old := range c.carts {
		if now.Sub(old.createdAt) > cartMaxAge {
			delete(c.carts, id)
		}
	}
	c.carts[cart.id] = cart
	c.mu.Unlock()
	return &merchant.Cart{ID: cart.id, Merchant: Name}, nil
}

func (c *Connector) getCart(cartID string) (*localCart, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cart, ok := c.carts[cartID]
	if !ok {
		return nil, fmt.Errorf("%w: swiggy_instamart cart %s", shared.ErrNotFound, cartID)
	}
	return cart, nil
}

func (c *Connector) AddToCart(ctx context.Context, cartID, merchantProductID string, qty int) (*merchant.Cart, error) {
	if qty <= 0 {
		return nil, fmt.Errorf("swiggy_instamart: quantity must be positive, got %d", qty)
	}
	spinID, skuID, err := decodeProductID(merchantProductID)
	if err != nil {
		return nil, err
	}
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()
	next := append([]cartLine(nil), cart.lines...)
	found := false
	for i := range next {
		if next[i].spinID == spinID {
			next[i].qty += qty
			found = true
		}
	}
	if !found {
		next = append(next, cartLine{spinID: spinID, skuID: skuID, qty: qty})
	}
	if err := c.pushCart(ctx, cart, next); err != nil {
		return nil, err
	}
	return cartView(cart), nil
}

func (c *Connector) RemoveFromCart(ctx context.Context, cartID, merchantProductID string) (*merchant.Cart, error) {
	spinID, _, err := decodeProductID(merchantProductID)
	if err != nil {
		return nil, err
	}
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()
	var next []cartLine
	for _, l := range cart.lines {
		if l.spinID != spinID {
			next = append(next, l)
		}
	}
	if err := c.pushCart(ctx, cart, next); err != nil {
		return nil, err
	}
	return cartView(cart), nil
}

// pushCart replaces the account's Swiggy cart with lines and adopts what
// Swiggy actually accepted. A capped quantity is reflected locally; an item
// Swiggy dropped is an error, not a silently smaller cart. Caller holds
// remoteMu.
func (c *Connector) pushCart(ctx context.Context, cart *localCart, lines []cartLine) error {
	if len(lines) == 0 {
		if _, err := call[json.RawMessage](ctx, c, "clear_cart", map[string]any{}); err != nil {
			return err
		}
		cart.lines = nil
		return nil
	}
	items := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		item := map[string]any{"spinId": l.spinID, "quantity": l.qty}
		if l.skuID != "" {
			item["skuId"] = l.skuID
		}
		items = append(items, item)
	}
	data, err := call[cartData](ctx, c, "update_cart", map[string]any{"selectedAddressId": cart.addressID, "items": items})
	if err != nil {
		return err
	}
	accepted := map[string]int{}
	for _, it := range data.Items {
		accepted[it.SpinID] += it.Quantity
	}
	var adopted []cartLine
	refused := 0
	for _, l := range lines {
		q := accepted[l.spinID]
		if q <= 0 {
			refused++
			continue
		}
		adopted = append(adopted, cartLine{spinID: l.spinID, skuID: l.skuID, qty: q})
	}
	cart.lines = adopted
	if refused > 0 {
		return fmt.Errorf("swiggy_instamart: Swiggy did not accept %d item(s) (out of stock or unserviceable at the selected address)", refused)
	}
	return nil
}

func cartView(cart *localCart) *merchant.Cart {
	items := make([]merchant.CartItem, 0, len(cart.lines))
	for _, l := range cart.lines {
		items = append(items, merchant.CartItem{MerchantProductID: encodeProductID(l.spinID, l.skuID), Quantity: l.qty})
	}
	return &merchant.Cart{ID: cart.id, Merchant: Name, Items: items}
}

// GetDeliveryOptions is where the intent's shipping ALIAS meets the linked
// account: the cart is only checkout-eligible if the alias is the one the
// user mapped to their saved Swiggy address at link time. Swiggy's MCP has
// no separate delivery-option tool — fees and slot arrive in get_cart's
// bill — so no options are returned.
func (c *Connector) GetDeliveryOptions(_ context.Context, cartID, shippingAlias string) ([]merchant.DeliveryOption, error) {
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	addressID, alias, err := c.settings()
	if err != nil {
		return nil, err
	}
	if alias == "" || shippingAlias != alias {
		return nil, fmt.Errorf("swiggy_instamart: shipping alias %q is not linked to a saved Swiggy address (linked alias: %q); re-run merchant-login with -alias", shippingAlias, alias)
	}
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()
	if cart.addressID != addressID {
		return nil, errors.New("swiggy_instamart: this cart was built for a different saved address; re-run discovery")
	}
	cart.aliasVerified = true
	return []merchant.DeliveryOption{}, nil
}

type billLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type cartItem struct {
	SpinID                string  `json:"spinId"`
	SkuID                 string  `json:"skuId"`
	ItemName              string  `json:"itemName"`
	ItemVariant           string  `json:"itemVariant"`
	Quantity              int     `json:"quantity"`
	IsInStockAndAvailable bool    `json:"isInStockAndAvailable"`
	DiscountedFinalPrice  float64 `json:"discountedFinalPrice"`
}

// cartData decodes only what Algebra needs from get_cart/update_cart. The
// selected address's name, phone number and street lines are deliberately
// not decoded — only its ID.
type cartData struct {
	SelectedAddressDetails *struct {
		ID string `json:"id"`
	} `json:"selectedAddressDetails"`
	Items         []cartItem `json:"items"`
	BillBreakdown struct {
		LineItems []billLine `json:"lineItems"`
		ToPay     billLine   `json:"toPay"`
	} `json:"billBreakdown"`
	AddressWarning     string            `json:"addressWarning"`
	UnserviceableItems []json.RawMessage `json:"unserviceableItems"`
	CartAbsent         bool              `json:"cartAbsent"`
	CartAbsentReason   string            `json:"cartAbsentReason"`
	CartWarning        *struct {
		Message string `json:"message"`
	} `json:"cartWarning"`
}

// verifyRemote checks the live Swiggy cart is deliverable, addressed to the
// linked address, and exactly what Algebra put there.
func verifyRemote(cart *localCart, data *cartData) error {
	switch {
	case data.CartAbsent:
		return fmt.Errorf("swiggy_instamart: Swiggy reports no cart (%s)", remotemcp.SafeText(data.CartAbsentReason, 200))
	case data.CartWarning != nil && data.CartWarning.Message != "":
		return fmt.Errorf("swiggy_instamart: Swiggy cart warning: %s", remotemcp.SafeText(data.CartWarning.Message, 200))
	case data.AddressWarning != "":
		return fmt.Errorf("swiggy_instamart: Swiggy address warning: %s", remotemcp.SafeText(data.AddressWarning, 200))
	case len(data.UnserviceableItems) > 0:
		return errors.New("swiggy_instamart: some items are unserviceable at the selected address")
	}
	if d := data.SelectedAddressDetails; d != nil && d.ID != "" && d.ID != cart.addressID {
		return errors.New("swiggy_instamart: the Swiggy cart is addressed to a different saved address than the linked one")
	}
	want := map[string]int{}
	for _, l := range cart.lines {
		want[l.spinID] += l.qty
	}
	if len(want) == 0 {
		return errors.New("swiggy_instamart: cart is empty")
	}
	got := map[string]int{}
	for _, it := range data.Items {
		if !it.IsInStockAndAvailable {
			return fmt.Errorf("swiggy_instamart: %s is no longer available", remotemcp.SafeText(it.ItemName, 80))
		}
		got[it.SpinID] += it.Quantity
	}
	if !maps.Equal(want, got) {
		return errors.New("swiggy_instamart: the Swiggy cart no longer matches what Algebra added (it may have been edited in the Swiggy app); re-run discovery")
	}
	return nil
}

func (c *Connector) GetCheckoutQuote(ctx context.Context, cartID string) (*quote.CheckoutQuote, error) {
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()
	data, err := call[cartData](ctx, c, "get_cart", map[string]any{})
	if err != nil {
		return nil, err
	}
	if err := verifyRemote(cart, &data); err != nil {
		return nil, err
	}
	return buildQuote(cartID, &data, c.now())
}

// buildQuote maps Swiggy's bill onto CheckoutQuote. Labelled lines fill the
// named components; whatever the labels don't explain is reconciled into
// OtherFee (or ItemDiscounts, if negative), so FinalPayable is always
// exactly Swiggy's own "To Pay" — the one number that is actually charged.
func buildQuote(cartID string, data *cartData, now time.Time) (*quote.CheckoutQuote, error) {
	toPay, err := parseDisplayAmount(data.BillBreakdown.ToPay.Value)
	if err != nil {
		return nil, err
	}
	inr := func(v int64) money.Amount { return money.Amount{MinorUnits: v, Currency: "INR"} }

	var itemsTotal int64
	items := make([]quote.Item, 0, len(data.Items))
	for _, it := range data.Items {
		unit, ok := rupeesToPaise(it.DiscountedFinalPrice)
		if !ok {
			return nil, fmt.Errorf("swiggy_instamart: unreadable price for %s", remotemcp.SafeText(it.ItemName, 80))
		}
		name := it.ItemName
		if it.ItemVariant != "" {
			name += " (" + it.ItemVariant + ")"
		}
		items = append(items, quote.Item{
			MerchantProductID: encodeProductID(it.SpinID, it.SkuID),
			Name:              remotemcp.SafeText(name, 160),
			Quantity:          it.Quantity,
			UnitPrice:         inr(unit),
		})
		itemsTotal += unit * int64(it.Quantity)
	}

	var subtotal, discounts, delivery, handling, platform, tax int64
	subtotalFromBill := false
	for _, line := range data.BillBreakdown.LineItems {
		amt, err := parseDisplayAmount(line.Value)
		if err != nil {
			continue // reconciliation below keeps the total exact regardless
		}
		label := strings.ToLower(line.Label)
		switch {
		case strings.Contains(label, "item total") || strings.Contains(label, "subtotal"):
			subtotal, subtotalFromBill = abs(amt), true
		case amt < 0 || strings.Contains(label, "discount") || strings.Contains(label, "saving") || strings.Contains(label, "coupon"):
			discounts += abs(amt)
		case strings.Contains(label, "delivery"):
			delivery += amt
		case strings.Contains(label, "handling"):
			handling += amt
		case strings.Contains(label, "platform"):
			platform += amt
		case strings.Contains(label, "gst") || strings.Contains(label, "tax"):
			tax += amt
		}
	}
	if !subtotalFromBill {
		subtotal = itemsTotal
	}
	other := toPay - (subtotal - discounts + delivery + handling + platform + tax)
	if other < 0 {
		discounts += -other
		other = 0
	}

	q := &quote.CheckoutQuote{
		QuoteID:                   "swiggyim_quote_" + uuid.NewString(),
		CartID:                    cartID,
		Merchant:                  Name,
		Items:                     items,
		Subtotal:                  inr(subtotal),
		ItemDiscounts:             inr(discounts),
		DeliveryFee:               inr(delivery),
		HandlingFee:               inr(handling),
		PlatformFee:               inr(platform),
		Tax:                       inr(tax),
		OtherFee:                  inr(other),
		PaymentSourceRequirements: []string{PaymentRequirementCOD},
		RetrievedAt:               now,
		ExpiresAt:                 now.Add(quoteTTL),
	}
	q.Recompute()
	if q.FinalPayable.MinorUnits != toPay {
		return nil, errors.New("swiggy_instamart: quote does not reconcile to Swiggy's payable amount")
	}
	return q, nil
}

type paymentOptions struct {
	COD *struct {
		Available bool `json:"available"`
	} `json:"cod"`
}

type checkoutData struct {
	OrderID string `json:"orderId"`
	Status  string `json:"status"`
	PaasID  string `json:"paasId"`
	Orders  []struct {
		OrderID string `json:"orderId"`
		Status  string `json:"status"`
		Error   string `json:"error"`
	} `json:"orders"`
	FailureCount int   `json:"failureCount"`
	AllSucceeded *bool `json:"allSucceeded"`
}

func needsUser(reason string) *merchant.ExecutionResult {
	return &merchant.ExecutionResult{Status: merchant.ExecutionUserInterventionNeeded, Reason: reason}
}

func needsMerchant(reason string) *merchant.ExecutionResult {
	return &merchant.ExecutionResult{Status: merchant.ExecutionMerchantInterventionNeeded, Reason: reason}
}

// ExecuteCheckout places a REAL Swiggy Instamart order, Cash on Delivery.
// Every precondition is re-verified against Swiggy immediately before the
// order call, and every ambiguous outcome is reported as needing a human —
// never as a failure that would invite a duplicate retry.
func (c *Connector) ExecuteCheckout(ctx context.Context, cartID, _ string, fulfillment merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	if !c.ready() {
		return needsUser(c.Status().Detail), nil
	}
	cart, err := c.getCart(cartID)
	if err != nil {
		return nil, err
	}
	c.remoteMu.Lock()
	defer c.remoteMu.Unlock()

	if !cart.aliasVerified {
		return needsUser("the order's shipping alias was never matched to the linked Swiggy address"), nil
	}
	if fulfillment.Shipping == nil {
		return needsUser("no shipping profile was resolved for this order"), nil
	}

	data, err := call[cartData](ctx, c, "get_cart", map[string]any{})
	if err != nil {
		return nil, err
	}
	if err := verifyRemote(cart, &data); err != nil {
		return needsUser(err.Error()), nil
	}
	q, err := buildQuote(cartID, &data, c.now())
	if err != nil {
		return nil, err
	}

	matches, err := c.addressMatchesProfile(ctx, cart.addressID, fulfillment.Shipping.PostalCode)
	if err != nil {
		return nil, err
	}
	if !matches {
		return needsUser("the linked Swiggy address is missing or not in the approved shipping profile's PIN code; re-link with the right address"), nil
	}

	opts, err := call[paymentOptions](ctx, c, "get_payment_options", map[string]any{})
	if err != nil {
		return nil, err
	}
	if opts.COD == nil || !opts.COD.Available {
		return needsUser("Cash on Delivery isn't offered for this cart, and Algebra never pays Swiggy on the user's behalf; finish this order in the Swiggy app"), nil
	}

	placed, err := call[checkoutData](ctx, c, "checkout", map[string]any{"addressId": cart.addressID, "paymentMethod": "COD"})
	var toolErr *remotemcp.ToolError
	switch {
	case errors.As(err, &toolErr):
		return &merchant.ExecutionResult{Status: merchant.ExecutionFailed, Reason: "Swiggy declined the order: " + toolErr.Message}, nil
	case errors.Is(err, remotemcp.ErrSessionExpired):
		return needsUser("the Swiggy session expired before the order was accepted; re-link and retry"), nil
	case err != nil:
		return needsMerchant("checkout outcome unknown (" + remotemcp.SafeText(err.Error(), 200) + "); check Instamart orders in the Swiggy app before retrying"), nil
	}
	return c.interpretCheckout(cartID, q, &placed), nil
}

var orderIDRE = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,64}$`)

func (c *Connector) interpretCheckout(cartID string, q *quote.CheckoutQuote, d *checkoutData) *merchant.ExecutionResult {
	if d.PaasID != "" || strings.EqualFold(d.Status, "PENDING_PAYMENT") {
		return needsMerchant("Swiggy created an order awaiting online payment although Cash on Delivery was requested; complete or cancel it in the Swiggy app")
	}
	var ids []string
	if len(d.Orders) > 0 {
		failures := 0
		for _, o := range d.Orders {
			if o.Error == "" && orderIDRE.MatchString(o.OrderID) {
				ids = append(ids, o.OrderID)
			} else {
				failures++
			}
		}
		if d.AllSucceeded == nil || !*d.AllSucceeded || failures > 0 || d.FailureCount > 0 {
			return needsMerchant(fmt.Sprintf("Swiggy split this cart across %d stores and placed %d order(s) (%s); review them in the Swiggy app", len(d.Orders), len(ids), strings.Join(ids, ", ")))
		}
	} else if orderIDRE.MatchString(d.OrderID) {
		ids = []string{d.OrderID}
	}
	if len(ids) == 0 {
		return needsMerchant("Swiggy's checkout response carried no usable order ID; check Instamart orders in the Swiggy app before retrying")
	}

	items := make([]order.Item, len(q.Items))
	for i, it := range q.Items {
		items[i] = order.Item{MerchantProductID: it.MerchantProductID, Name: it.Name, Quantity: it.Quantity, UnitPrice: it.UnitPrice}
	}
	c.mu.Lock()
	delete(c.carts, cartID)
	c.mu.Unlock()
	return &merchant.ExecutionResult{
		Status: merchant.ExecutionSucceeded,
		Order: &order.Order{
			Merchant:        Name,
			MerchantOrderID: strings.Join(ids, ","),
			Items:           items,
			Total:           q.FinalPayable,
			Status:          order.StatusPlaced,
			PlacedAt:        c.now(),
		},
	}
}

type addressPage struct {
	Addresses []struct {
		ID              string `json:"id"`
		AddressLine     string `json:"addressLine"`
		AddressCategory string `json:"addressCategory"`
		AddressTag      string `json:"addressTag"`
	} `json:"addresses"`
	Pagination struct {
		HasMore bool `json:"hasMore"`
	} `json:"pagination"`
}

const maxAddressPages = 10

var pinRE = regexp.MustCompile(`\b[1-9][0-9]{5}\b`)

// addressMatchesProfile confirms the linked saved address still exists and,
// when both sides carry a PIN code, that they agree. The address line is
// compared in memory and discarded — never logged or returned.
func (c *Connector) addressMatchesProfile(ctx context.Context, addressID, postalCode string) (bool, error) {
	for page := 1; page <= maxAddressPages; page++ {
		data, err := call[addressPage](ctx, c, "get_addresses", map[string]any{"page": page})
		if err != nil {
			return false, err
		}
		for _, a := range data.Addresses {
			if a.ID == addressID {
				return pinCompatible(a.AddressLine, postalCode), nil
			}
		}
		if !data.Pagination.HasMore {
			break
		}
	}
	return false, nil
}

func pinCompatible(addressLine, postalCode string) bool {
	want := strings.ReplaceAll(strings.TrimSpace(postalCode), " ", "")
	pins := pinRE.FindAllString(addressLine, -1)
	if want == "" || len(pins) == 0 {
		return true
	}
	for _, p := range pins {
		if p == want {
			return true
		}
	}
	return false
}

// SavedAddress is shown to the user in their own terminal by
// cmd/merchant-login so they can pick a delivery address. It is never
// stored, logged, or sent to an agent.
type SavedAddress struct {
	ID          string
	Label       string
	AddressLine string
}

// SavedAddresses lists the linked account's saved addresses.
func SavedAddresses(ctx context.Context, client *remotemcp.Client) ([]SavedAddress, error) {
	c := New(client)
	var out []SavedAddress
	for page := 1; page <= maxAddressPages; page++ {
		data, err := call[addressPage](ctx, c, "get_addresses", map[string]any{"page": page})
		if err != nil {
			return nil, err
		}
		for _, a := range data.Addresses {
			label := a.AddressTag
			if label == "" {
				label = a.AddressCategory
			}
			out = append(out, SavedAddress{ID: a.ID, Label: label, AddressLine: a.AddressLine})
		}
		if !data.Pagination.HasMore {
			break
		}
	}
	return out, nil
}

type ordersData struct {
	Orders []struct {
		OrderID       string  `json:"orderId"`
		Status        string  `json:"status"`
		CreatedAt     string  `json:"createdAt"`
		TotalAmount   float64 `json:"totalAmount"`
		IsActive      bool    `json:"isActive"`
		CurrentStatus string  `json:"currentStatus"`
		HistoryStatus string  `json:"historyStatus"`
		Items         []struct {
			Name     string `json:"name"`
			Quantity int    `json:"quantity"`
			ItemID   string `json:"itemId"`
		} `json:"items"`
	} `json:"orders"`
}

// GetOrder reads order state from get_orders (Swiggy's history covers the
// last 15 days). A multi-store checkout's comma-joined ID is reported as one
// order whose status is the least-advanced of its parts.
func (c *Connector) GetOrder(ctx context.Context, merchantOrderID string) (*order.Order, error) {
	data, err := call[ordersData](ctx, c, "get_orders", map[string]any{"count": 20})
	if err != nil {
		return nil, err
	}
	ord := &order.Order{Merchant: Name, MerchantOrderID: merchantOrderID, Total: money.Amount{Currency: "INR"}}
	var statuses []order.Status
	for _, id := range strings.Split(merchantOrderID, ",") {
		found := false
		for _, o := range data.Orders {
			if o.OrderID != id {
				continue
			}
			found = true
			if paise, ok := rupeesToPaise(o.TotalAmount); ok {
				ord.Total.MinorUnits += paise
			}
			for _, it := range o.Items {
				ord.Items = append(ord.Items, order.Item{MerchantProductID: it.ItemID, Name: remotemcp.SafeText(it.Name, 160), Quantity: it.Quantity, UnitPrice: money.Amount{Currency: "INR"}})
			}
			if placed, err := time.Parse(time.RFC3339, o.CreatedAt); err == nil && (ord.PlacedAt.IsZero() || placed.Before(ord.PlacedAt)) {
				ord.PlacedAt = placed
			}
			statuses = append(statuses, mapOrderStatus(o.IsActive, o.Status+" "+o.CurrentStatus+" "+o.HistoryStatus))
		}
		if !found {
			return nil, fmt.Errorf("%w: swiggy_instamart order %s (not in the last 15 days of history)", shared.ErrNotFound, id)
		}
	}
	ord.Status = combineStatuses(statuses)
	return ord, nil
}

// mapOrderStatus is best-effort: Swiggy documents status fields but not
// their values, so anything unrecognised stays PLACED rather than being
// promoted to a state Algebra can't confirm.
func mapOrderStatus(active bool, text string) order.Status {
	s := strings.ToLower(text)
	switch {
	case strings.Contains(s, "cancel"):
		return order.StatusCancelled
	case strings.Contains(s, "fail"):
		return order.StatusFailed
	case !active && strings.Contains(s, "deliver"):
		return order.StatusDelivered
	case strings.Contains(s, "out for delivery") || strings.Contains(s, "picked") || strings.Contains(s, "dispatch"):
		return order.StatusShipped
	case strings.Contains(s, "confirm"):
		return order.StatusConfirmed
	default:
		return order.StatusPlaced
	}
}

func combineStatuses(statuses []order.Status) order.Status {
	rank := map[order.Status]int{
		order.StatusFailed: 0, order.StatusCancelled: 1, order.StatusPlaced: 2,
		order.StatusConfirmed: 3, order.StatusShipped: 4, order.StatusDelivered: 5,
	}
	if len(statuses) == 0 {
		return order.StatusPlaced
	}
	least := statuses[0]
	for _, s := range statuses[1:] {
		if rank[s] < rank[least] {
			least = s
		}
	}
	return least
}

func (c *Connector) CancelOrder(context.Context, string) error {
	return fmt.Errorf("%w: Swiggy's MCP offers no cancellation tool and directs Instamart cancellations to Swiggy customer care", shared.ErrNotImplemented)
}

func encodeProductID(spinID, skuID string) string {
	return url.QueryEscape(spinID) + "|" + url.QueryEscape(skuID)
}

func decodeProductID(id string) (spinID, skuID string, err error) {
	rawSpin, rawSku, _ := strings.Cut(id, "|")
	spinID, err1 := url.QueryUnescape(rawSpin)
	skuID, err2 := url.QueryUnescape(rawSku)
	if err1 != nil || err2 != nil || spinID == "" {
		return "", "", fmt.Errorf("swiggy_instamart: malformed product id %q", id)
	}
	return spinID, skuID, nil
}

// rupeesToPaise converts a rupee amount Swiggy reports as a JSON number.
func rupeesToPaise(v float64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 10_000_000 {
		return 0, false
	}
	return int64(math.Round(v * 100)), true
}

var amountCharsRE = regexp.MustCompile(`[^0-9.\-]`)

// parseDisplayAmount reads a bill value such as "₹1,234.50", "-₹20" or
// "FREE" into paise.
func parseDisplayAmount(s string) (int64, error) {
	if strings.Contains(strings.ToLower(s), "free") {
		return 0, nil
	}
	cleaned := amountCharsRE.ReplaceAllString(s, "")
	if cleaned == "" || strings.Count(cleaned, ".") > 1 || strings.LastIndex(cleaned, "-") > 0 {
		return 0, fmt.Errorf("swiggy_instamart: unreadable amount %q", remotemcp.SafeText(s, 40))
	}
	f, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, fmt.Errorf("swiggy_instamart: unreadable amount %q", remotemcp.SafeText(s, 40))
	}
	paise, ok := rupeesToPaise(math.Abs(f))
	if !ok {
		return 0, fmt.Errorf("swiggy_instamart: amount out of range %q", remotemcp.SafeText(s, 40))
	}
	if f < 0 {
		paise = -paise
	}
	return paise, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
	_ merchant.Warmer         = (*Connector)(nil)
)
