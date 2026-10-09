// Package democheckout is the store demo accounts buy from: real product
// listings, simulated checkout. Products and prices come from the same live
// web search the agent shows the user (store, title, listed price, link);
// the cart, quote, order, order number and delivery estimate are simulated
// and no money moves. Every order it produces is provider_mode=mock and
// its number starts "DEMO-", so it can never pass for a real purchase.
//
// Only demo accounts are routed here (DiscoveryService's mode filter); a
// live account never sees it.
package democheckout

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/websearch"
)

const (
	Name = "demo_checkout"

	// searchLimit matches the agent's own web_search limit, so a search here
	// for the same query is answered from the shared search cache.
	searchLimit = 5
	// rememberFor/maxRemembered bound the listings kept from the agent's
	// searches (public web data, no user information).
	rememberFor   = 2 * time.Hour
	maxRemembered = 2000
	// matchThreshold is the share of the query's words a remembered
	// listing's title must contain to count as the same product (on top of
	// the brand and model numbers, which must all match — see recall).
	matchThreshold = 0.75
)

// SearchFunc runs a live web product search (DiscoveryService's cached web
// search in production wiring).
type SearchFunc func(ctx context.Context, query string, limit int) ([]websearch.Result, error)

type listing struct {
	id, title, store, url, image string
	priceMinor                   int64
	words                        map[string]bool
	seen                         time.Time
}

type cartState struct {
	items []merchant.CartItem
}

// Connector is safe for concurrent use.
type Connector struct {
	search SearchFunc
	now    func() time.Time

	mu       sync.Mutex
	listings map[string]*listing
	carts    map[string]*cartState
	orders   map[string]*order.Order
}

// New returns the demo checkout. search may be nil (no web search
// configured), in which case it reports itself not ready.
func New(search SearchFunc) *Connector {
	return &Connector{
		search: search, now: time.Now,
		listings: map[string]*listing{}, carts: map[string]*cartState{}, orders: map[string]*order.Order{},
	}
}

func (c *Connector) Name() string                { return Name }
func (c *Connector) Mode() merchant.ProviderMode { return merchant.ProviderModeMock }

func (c *Connector) Capabilities() merchant.Capabilities {
	ready := c.search != nil
	return merchant.Capabilities{Search: ready, Cart: ready, Checkout: ready, OrderTracking: ready}
}

func (c *Connector) Status() merchant.Status {
	if c.search == nil {
		return merchant.Status{Integration: merchant.IntegrationMock, Ready: false,
			Detail: "Demo checkout needs live web search (set GEMINI_API_KEY) to find real listings."}
	}
	return merchant.Status{Integration: merchant.IntegrationMock, Ready: true,
		Detail: "Simulated checkout for demo accounts only: real listings from the web, fake money, nothing ships."}
}

// Remember records listings the agent's own web search found, so buying
// "that one" matches the exact listing the user was shown — same store,
// same price — without searching again.
func (c *Connector) Remember(results []websearch.Result) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range results {
		if l := toListing(r, now); l != nil {
			c.listings[l.id] = l
		}
	}
	c.pruneLocked(now)
}

func toListing(r websearch.Result, now time.Time) *listing {
	title := strings.TrimSpace(r.Title)
	if title == "" || r.PriceMinorUnits <= 0 || (r.Currency != "" && r.Currency != "INR") {
		return nil
	}
	// Even with pretend money, a demo never "buys" a listing flagged as
	// priced far below everyone else — it's what a scam looks like.
	if slices.Contains(r.Warnings, websearch.WarnFarBelowOthers) {
		return nil
	}
	sum := sha256.Sum256([]byte(r.URL + "|" + title + "|" + r.Store))
	store := strings.TrimSpace(r.Store)
	if store == "" {
		store = "an online store"
	}
	return &listing{
		id: "demo_" + hex.EncodeToString(sum[:8]), title: title, store: store, url: r.URL, image: r.ImageURL,
		priceMinor: r.PriceMinorUnits, words: words(title), seen: now,
	}
}

func (c *Connector) pruneLocked(now time.Time) {
	for id, l := range c.listings {
		if now.Sub(l.seen) > rememberFor {
			delete(c.listings, id)
		}
	}
	if len(c.listings) <= maxRemembered {
		return
	}
	all := make([]*listing, 0, len(c.listings))
	for _, l := range c.listings {
		all = append(all, l)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seen.Before(all[j].seen) })
	for _, l := range all[:len(all)-maxRemembered] {
		delete(c.listings, l.id)
	}
}

func (c *Connector) Authenticate(context.Context, merchant.AuthRequest) (*merchant.AuthResult, error) {
	return &merchant.AuthResult{Authenticated: true}, nil
}

// SearchProducts answers from listings the agent already showed when one is
// the product the query names, and otherwise runs a live web search held
// to the same test. No listing that is that product means no result — the
// demo checkout never quotes a substitute the user didn't pick.
func (c *Connector) SearchProducts(ctx context.Context, query string, limit int) ([]merchant.Product, error) {
	if c.search == nil {
		return nil, fmt.Errorf("%w: demo checkout needs live web search (GEMINI_API_KEY)", shared.ErrNotImplemented)
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	if hits := c.recall(query, limit); len(hits) > 0 {
		return hits, nil
	}
	results, err := c.search(ctx, query, searchLimit)
	if err != nil {
		return nil, err
	}
	c.Remember(results)
	return c.recall(query, limit), nil
}

// recall returns remembered listings that are the product the query names,
// best match first. A listing must carry every model number in the query
// ("m171", "750ml") and its first word — usually the brand — and most of
// the rest: "Logitech M171 wireless optical mouse" must never recall some
// other brand's wireless optical mouse.
func (c *Connector) recall(query string, limit int) []merchant.Product {
	query, store := c.splitStore(query)
	want := words(query)
	if len(want) == 0 {
		return nil
	}
	required := map[string]bool{}
	for w := range want {
		if strings.ContainsFunc(w, unicode.IsDigit) {
			required[w] = true
		}
	}
	if first := firstWord(query); first != "" {
		required[first] = true
	}
	type scored struct {
		l     *listing
		score float64
	}
	c.mu.Lock()
	var hits []scored
	for _, l := range c.listings {
		n, missing := 0, false
		for w := range want {
			switch {
			case l.words[w]:
				n++
			case required[w]:
				missing = true
			}
		}
		if missing || (store != "" && !strings.Contains(strings.ToLower(l.store), store)) {
			continue
		}
		if score := float64(n) / float64(len(want)); score >= matchThreshold {
			hits = append(hits, scored{l, score})
		}
	}
	c.mu.Unlock()
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].l.seen.After(hits[j].l.seen)
	})
	out := make([]merchant.Product, 0, min(limit, len(hits)))
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, h.l.product(min(0.6+0.35*h.score, 0.95)))
	}
	return out
}

// splitStore separates a trailing "from <store>" — how the agent names the
// listing a user picked ("Coca-Cola Zero Sugar PET from Zepto") — from the
// product words, so two stores' identical titles resolve to the one chosen.
// It only counts as a store when a remembered listing is from that store;
// otherwise "Dark chocolate from Belgium" stays a product name.
func (c *Connector) splitStore(query string) (string, string) {
	i := strings.LastIndex(strings.ToLower(query), " from ")
	if i <= 0 {
		return query, ""
	}
	store := strings.ToLower(strings.TrimSpace(query[i+len(" from "):]))
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.listings {
		if store != "" && strings.Contains(strings.ToLower(l.store), store) {
			return strings.TrimSpace(query[:i]), store
		}
	}
	return query, ""
}

func (l *listing) product(confidence float64) merchant.Product {
	return merchant.Product{
		MerchantProductID: l.id, Merchant: Name, Brand: l.store, Name: l.title,
		PriceMinorUnits: l.priceMinor, Currency: "INR", Available: true, URL: l.url, Confidence: confidence,
	}
}

func firstWord(s string) string {
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= 2 {
			return w
		}
	}
	return ""
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= 2 {
			out[w] = true
		}
	}
	return out
}

func (c *Connector) listing(id string) (*listing, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.listings[id]
	if !ok {
		return nil, fmt.Errorf("%w: demo listing %s (search again)", shared.ErrNotFound, id)
	}
	return l, nil
}

func (c *Connector) GetProduct(_ context.Context, id string) (*merchant.Product, error) {
	l, err := c.listing(id)
	if err != nil {
		return nil, err
	}
	p := l.product(1)
	return &p, nil
}

func (c *Connector) GetOffers(context.Context, string) ([]quote.Offer, error) { return nil, nil }

func (c *Connector) CreateCart(context.Context, string) (*merchant.Cart, error) {
	id := "democart_" + uuid.NewString()
	c.mu.Lock()
	c.carts[id] = &cartState{}
	c.mu.Unlock()
	return &merchant.Cart{ID: id, Merchant: Name}, nil
}

func (c *Connector) cart(id string) (*cartState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cs, ok := c.carts[id]
	if !ok {
		return nil, fmt.Errorf("%w: demo cart %s", shared.ErrNotFound, id)
	}
	return cs, nil
}

func (c *Connector) AddToCart(_ context.Context, cartID, productID string, qty int) (*merchant.Cart, error) {
	if qty <= 0 {
		return nil, fmt.Errorf("demo checkout: quantity must be positive")
	}
	if _, err := c.listing(productID); err != nil {
		return nil, err
	}
	cs, err := c.cart(cartID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range cs.items {
		if cs.items[i].MerchantProductID == productID {
			cs.items[i].Quantity += qty
			return c.viewLocked(cartID, cs), nil
		}
	}
	cs.items = append(cs.items, merchant.CartItem{MerchantProductID: productID, Quantity: qty})
	return c.viewLocked(cartID, cs), nil
}

func (c *Connector) RemoveFromCart(_ context.Context, cartID, productID string) (*merchant.Cart, error) {
	cs, err := c.cart(cartID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := cs.items[:0]
	for _, it := range cs.items {
		if it.MerchantProductID != productID {
			kept = append(kept, it)
		}
	}
	cs.items = kept
	return c.viewLocked(cartID, cs), nil
}

func (c *Connector) viewLocked(cartID string, cs *cartState) *merchant.Cart {
	items := make([]merchant.CartItem, len(cs.items))
	copy(items, cs.items)
	return &merchant.Cart{ID: cartID, Merchant: Name, Items: items}
}

// quickCommerce stores deliver in minutes; everything else in days. Used
// only to make the simulated delivery estimate believable.
var quickCommerce = []string{"blinkit", "zepto", "instamart", "swiggy", "bigbasket", "bbnow", "flipkart minutes", "dunzo"}

func isQuick(store string) bool {
	s := strings.ToLower(store)
	for _, q := range quickCommerce {
		if strings.Contains(s, q) {
			return true
		}
	}
	return false
}

func (c *Connector) GetDeliveryOptions(_ context.Context, cartID string, _ string) ([]merchant.DeliveryOption, error) {
	q, err := c.GetCheckoutQuote(context.Background(), cartID)
	if err != nil {
		return nil, err
	}
	return []merchant.DeliveryOption{{ID: "standard", Label: "Standard delivery (simulated)", FeeMinorUnits: q.DeliveryFee.MinorUnits, ETA: q.DeliveryETA}}, nil
}

func (c *Connector) ApplyCoupon(context.Context, string, string) (*merchant.Cart, error) {
	return nil, fmt.Errorf("%w: demo checkout has no coupons", shared.ErrNotImplemented)
}

// GetCheckoutQuote prices the cart at the listed prices, with a simulated
// delivery fee and estimate: quick-commerce stores free over ₹199 (else
// ₹30) in about 25 minutes, everyone else free over ₹499 (else ₹40) in two
// days.
func (c *Connector) GetCheckoutQuote(_ context.Context, cartID string) (*quote.CheckoutQuote, error) {
	cs, err := c.cart(cartID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	items := make([]merchant.CartItem, len(cs.items))
	copy(items, cs.items)
	c.mu.Unlock()
	if len(items) == 0 {
		return nil, fmt.Errorf("demo checkout: cart %s is empty", cartID)
	}

	var subtotal int64
	quick := true
	qItems := make([]quote.Item, 0, len(items))
	for _, it := range items {
		l, err := c.listing(it.MerchantProductID)
		if err != nil {
			return nil, err
		}
		subtotal += l.priceMinor * int64(it.Quantity)
		quick = quick && isQuick(l.store)
		qItems = append(qItems, quote.Item{
			MerchantProductID: l.id, Name: l.title + " — " + l.store, Quantity: it.Quantity,
			UnitPrice: money.Amount{MinorUnits: l.priceMinor, Currency: "INR"},
		})
	}
	now := c.now()
	fee, freeOver, eta := int64(4000), int64(49900), now.Add(48*time.Hour)
	if quick {
		fee, freeOver, eta = 3000, 19900, now.Add(25*time.Minute)
	}
	if subtotal >= freeOver {
		fee = 0
	}
	q := &quote.CheckoutQuote{
		QuoteID:     "demoquote_" + uuid.NewString(),
		CartID:      cartID,
		Merchant:    Name,
		Items:       qItems,
		Subtotal:    money.Amount{MinorUnits: subtotal, Currency: "INR"},
		DeliveryFee: money.Amount{MinorUnits: fee, Currency: "INR"},
		DeliveryETA: &eta,
		RetrievedAt: now,
		ExpiresAt:   now.Add(10 * time.Minute),
	}
	q.Recompute()
	return q, nil
}

// ExecuteCheckout "places" the order: same checks a real store would make
// (an address to ship to, a non-empty cart), a DEMO- order number, and the
// delivery estimate from the quote. No money moves.
func (c *Connector) ExecuteCheckout(ctx context.Context, cartID, _ string, f merchant.Fulfillment) (*merchant.ExecutionResult, error) {
	q, err := c.GetCheckoutQuote(ctx, cartID)
	if err != nil {
		return nil, err
	}
	if f.Shipping == nil {
		return &merchant.ExecutionResult{Status: merchant.ExecutionUserInterventionNeeded, Reason: "add a delivery address first"}, nil
	}
	items := make([]order.Item, len(q.Items))
	for i, it := range q.Items {
		items[i] = order.Item{MerchantProductID: it.MerchantProductID, Name: it.Name, Quantity: it.Quantity, UnitPrice: it.UnitPrice}
	}
	ord := &order.Order{
		Merchant:        Name,
		MerchantOrderID: "DEMO-" + randomCode(8),
		Items:           items,
		Total:           q.FinalPayable,
		Status:          order.StatusPlaced,
		PlacedAt:        c.now(),
		DeliveryETA:     q.DeliveryETA,
	}
	c.mu.Lock()
	c.orders[ord.MerchantOrderID] = ord
	delete(c.carts, cartID)
	c.mu.Unlock()
	return &merchant.ExecutionResult{Status: merchant.ExecutionSucceeded, Order: ord}, nil
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I

func randomCode(n int) string {
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			idx = big.NewInt(int64(i % len(codeAlphabet)))
		}
		b[i] = codeAlphabet[idx.Int64()]
	}
	return string(b)
}

func (c *Connector) GetOrder(_ context.Context, id string) (*order.Order, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.orders[id]
	if !ok {
		return nil, fmt.Errorf("%w: demo order %s", shared.ErrNotFound, id)
	}
	cp := *o
	return &cp, nil
}

func (c *Connector) CancelOrder(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.orders[id]
	if !ok {
		return fmt.Errorf("%w: demo order %s", shared.ErrNotFound, id)
	}
	o.Status = order.StatusCancelled
	return nil
}

var (
	_ merchant.Connector      = (*Connector)(nil)
	_ merchant.StatusReporter = (*Connector)(nil)
)
